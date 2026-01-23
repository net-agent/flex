import { md5 } from '../lib/md5.js';
import { Stream, StreamState } from './stream.js';
import { EventEmitter } from './event_emitter.js';
import { CipherUtils, IV_LEN, CHECKSUM_LEN, PACKET_CODE, SALT } from '../lib/cipher_utils.js';

const HANDSHAKE_TIMEOUT = 10000; // 10s

export class SecretStream extends EventEmitter {
    /**
     * @param {Stream} stream - The underlying Stream
     * @param {string} secret - Password for encryption
     */
    constructor(stream, secret) {
        super();
        this.stream = stream;

        this.secret = secret;

        // Cipher State
        this.encoder = null; // Encrypt (Write)
        this.decoder = null; // Decrypt (Read)

        // Hooks
        // this.onLog = (msg) => console.log("[SecretStream]", msg);
        this.onLog = () => { }; // Silence logs by default
        this.listeningToStreamErrors = false;

        // this.handlers removed, using super


        // Bind stream events
        this.stream.on('data', this.handleData.bind(this));
        this.stream.on('close', () => this.emit('close'));
        this.stream.on('error', (err) => this.emit('error', err));

        // Buffering for handshake
        this.handshaking = true;
        this.handshakeBuffer = new Uint8Array(0);
        this.resolveHandshake = null;
        this.rejectHandshake = null;

        // Decryption buffer (for stream processing)
        // Note: For simple CTR, we process chunk by chunk. 
        // We use event emitter pattern, so we emit data as soon as decrypted.
    }

    /**
     * Initiate Handshake
     * @returns {Promise<void>}
     */
    async init() {
        return new Promise((resolve, reject) => {
            this.resolveHandshake = resolve;
            this.rejectHandshake = reject;

            this.onLog("Starting Handshake...");

            // Timeout
            setTimeout(() => {
                if (this.handshaking) {
                    this.onLog("Handshake Timeout!");
                    this.rejectHandshake(new Error("handshake timeout"));
                }
            }, HANDSHAKE_TIMEOUT);

            this.sendHandshake().catch(err => {
                this.onLog(`Send Handshake Failed: ${err.message}`);
                this.rejectHandshake(err);
            });
        });
    }

    async sendHandshake() {
        // 1. Generate Local IV
        const iv = new Uint8Array(IV_LEN);
        crypto.getRandomValues(iv);

        // 2. Generate Checksum: MD5(IV + Password)
        const passwordBytes = new TextEncoder().encode(this.secret);
        const toHash = new Uint8Array(iv.length + passwordBytes.length);
        toHash.set(iv);
        toHash.set(passwordBytes, iv.length);
        const checksum = md5(toHash);

        // 3. Send: [CODE] [IV] [CHECKSUM]
        const buf = new Uint8Array(1 + IV_LEN + CHECKSUM_LEN);
        buf[0] = PACKET_CODE;
        buf.set(iv, 1);
        buf.set(checksum, 1 + IV_LEN);

        // Save Local IV for encoder creation later
        this.localIV = iv;

        this.onLog(`Sending Handshake. IV=${iv.slice(0, 4)}... Checksum=${checksum.slice(0, 4)}...`);

        // Write raw to stream
        await this.stream.write(buf);

    }

    handleData(chunk) {
        if (this.handshaking) {
            this.processHandshakeData(chunk);
        } else {
            this.processStreamData(chunk);
        }
    }

    processHandshakeData(chunk) {
        // Ensure chunk is Uint8Array
        let data = chunk;
        if (data instanceof ArrayBuffer) {
            data = new Uint8Array(data);
        }

        // Append to buffer
        const newBuf = new Uint8Array(this.handshakeBuffer.length + data.length);
        newBuf.set(this.handshakeBuffer);
        newBuf.set(data, this.handshakeBuffer.length);
        this.handshakeBuffer = newBuf;

        // Need 1 (Code) + 16 (IV) + 16 (Checksum) = 33 bytes
        if (this.handshakeBuffer.length >= 33) {
            this.onLog(`Processing Handshake Buffer. Len=${this.handshakeBuffer.length}`);

            const data = this.handshakeBuffer.slice(0, 33);
            const remaining = this.handshakeBuffer.slice(33);
            this.handshakeBuffer = remaining; // Should be empty ideally

            try {
                this.verifyHandshake(data);
                this.handshaking = false;
                this.onLog("Handshake Verified! Initializing Ciphers...");

                // Initialize Ciphers
                this.initCiphers().then(() => {
                    this.onLog("Ciphers Initialized. Connection Secure.");
                    this.resolveHandshake();
                    // Process any remaining early data
                    if (remaining.length > 0) {
                        this.processStreamData(remaining);
                    }
                }).catch(err => {
                    this.onLog(`Cipher Init Failed: ${err.message}`);
                    this.rejectHandshake(err);
                });

            } catch (e) {
                this.onLog(`Handshake Verification Failed: ${e.message}`);
                this.rejectHandshake(e);
            }
        } else {
            this.onLog(`Buffering Handshake Data. Cap=${this.handshakeBuffer.length}/33`);
        }
    }


    verifyHandshake(data) {
        if (data[0] !== PACKET_CODE) {
            throw new Error("invalid handshake code");
        }

        const iv = data.slice(1, 1 + IV_LEN);
        const remoteChecksum = data.slice(1 + IV_LEN, 1 + IV_LEN + CHECKSUM_LEN);

        // Verify Checksum
        const passwordBytes = new TextEncoder().encode(this.secret);
        const toHash = new Uint8Array(iv.length + passwordBytes.length);
        toHash.set(iv);
        toHash.set(passwordBytes, iv.length);
        const calcedChecksum = md5(toHash);

        for (let i = 0; i < CHECKSUM_LEN; i++) {
            if (remoteChecksum[i] !== calcedChecksum[i]) {
                throw new Error("invalid handshake checksum");
            }
        }

        this.remoteIV = iv;
    }

    async initCiphers() {
        this.key = await CipherUtils.deriveKey(this.secret);
    }

    async processStreamData(chunk) {
        if (!this.key) return; // Should not happen if logic is correct

        // Ensure chunk is Uint8Array
        let data = chunk;
        if (data instanceof ArrayBuffer) {
            data = new Uint8Array(data);
        } else if (ArrayBuffer.isView(data) && !(data instanceof Uint8Array)) {
            data = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
        }

        const output = await this.crypt(data, this.remoteIV, this.readOffset || 0);
        this.readOffset = (this.readOffset || 0) + data.length;

        // Emit decrypted data
        // Emit decrypted data
        if (output.byteLength > 0) {
            // this.onLog(`RX Decrypted: ${output.byteLength} bytes`);
            this.emit('data', output);
        } else {
            this.onLog("RX Decrypted Empty!");
        }
    }


    async write(data) {
        if (this.handshaking) throw new Error("handshaking");

        // Ensure data is Uint8Array
        let payload = data;
        if (typeof data === 'string') {
            payload = new TextEncoder().encode(data);
        } else if (data instanceof ArrayBuffer) {
            payload = new Uint8Array(data);
        }

        const output = await this.crypt(payload, this.localIV, this.writeOffset || 0);
        this.writeOffset = (this.writeOffset || 0) + payload.length;

        await this.stream.write(output);
    }

    async crypt(input, initialIV, byteOffset) {
        return CipherUtils.crypt(this.key, initialIV, byteOffset, input);
    }


    close() {
        this.stream.close();
    }

    // Event Emitter Implementation
    // emit/on removed, using super

    // Override on to hook underlying stream lazily or init
    on(event, handler) {
        super.on(event, handler);

        // Forward 'close' and 'error' from underlying stream if not already did
        if (event !== 'data' && !this.listeningToStreamErrors) {
            // We can't really "hook on demand" easily without duplication checks.
            // Easier to just hook once in constructor for close/error.
        }
        return this;
    }


    // --- Proxy Getters/Setters to underlying Stream ---
    get remoteIP() { return this.stream.remoteIP; }
    set remoteIP(v) { this.stream.remoteIP = v; }

    get remotePort() { return this.stream.remotePort; }
    set remotePort(v) { this.stream.remotePort = v; }

    get remoteDomain() { return this.stream.remoteDomain; }
    set remoteDomain(v) { this.stream.remoteDomain = v; }

    get localIP() { return this.stream.localIP; }
    set localIP(v) { this.stream.localIP = v; }

    get localPort() { return this.stream.localPort; }
    set localPort(v) { this.stream.localPort = v; }

    get localDomain() { return this.stream.localDomain; }
    set localDomain(v) { this.stream.localDomain = v; }
}
