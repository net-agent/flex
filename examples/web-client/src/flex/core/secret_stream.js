import { md5 } from '../lib/md5.js';
import { Stream, StreamState } from './stream.js';

// Constants matching Go 'cipherconn'
const IV_LEN = 16;
const CHECKSUM_LEN = 16;
const PACKET_CODE = 0x09;
const HANDSHAKE_TIMEOUT = 10000; // 10s
const SALT = "cipherconn-of-exchanger";

export class SecretStream {
    /**
     * @param {Stream} stream - The underlying Stream
     * @param {string} secret - Password for encryption
     */
    constructor(stream, secret) {
        this.stream = stream;
        this.secret = secret;

        // Cipher State
        this.encoder = null; // Encrypt (Write)
        this.decoder = null; // Decrypt (Read)

        // Hooks
        // Hooks
        // this.onLog = (msg) => console.log("[SecretStream]", msg);
        this.onLog = () => { }; // Silence logs by default
        this.listeningToStreamErrors = false;
        this.listeningToStreamErrors = false;

        this.handlers = {
            data: [],
            close: [],
            error: []
        };


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
        // Derive Key using HKDF-SHA1
        // Key material: secret
        // Salt: "cipherconn-of-exchanger"
        // Info: ""
        // Length: 16 bytes (128-bit key for AES)

        const enc = new TextEncoder();
        const keyMaterial = await crypto.subtle.importKey(
            "raw",
            enc.encode(this.secret),
            { name: "HKDF" },
            false,
            ["deriveKey"]
        );

        const salt = enc.encode(SALT);
        const info = new Uint8Array(0); // empty info matching Go's nil

        // Derive AES-CTR Key
        const key = await crypto.subtle.deriveKey(
            {
                name: "HKDF",
                hash: "SHA-1",
                salt: salt,
                info: info
            },
            keyMaterial,
            { name: "AES-CTR", length: 128 },
            false,
            ["encrypt", "decrypt"]
        );

        this.key = key;
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

        // AES-CTR Decryption (Read)
        // Note: WebCrypto AES-CTR is stateless for the full buffer, BUT assumes we pass the correct counter block.
        // Standard WebCrypto AES-CTR decryption `decrypt()` expects the *entire* message to decrypt at once if using the initial counter,
        // OR we need to manually manage the counter for streaming.

        // Go's cipher.NewCTR(block, iv) creates a stateful stream cipher.
        // WebCrypto `encrypt`/`decrypt` are "one-shot" operations. 
        // TO support streaming AES-CTR in WebCrypto, we need to track the counter.
        // Counter = IV (16 bytes). 
        // For each block (16 bytes) processed, increment counter.

        // Wait! Implementing streaming AES-CTR manually with correct counter increment using WebCrypto one-shot is complex.
        // ALTERNATIVE: Since we are in a browser context (or simulation), maybe we can assume the chunk is the full payload?
        // NO, tcp stream is chunked.

        // We must implement the counter increment manually.
        // The IV is used as the initial counter.
        // 128-bit counter (big endian usually for CTR).
        // Default AES-CTR increments the lower 64-bits (or full 128?).
        // Go's generic `cipher.NewCTR` increments the *entire* 128-bit counter (starting from IV).

        // Let's implement a software XOR keystream generator using AES-ECB (if available) or 
        // re-use AES-CTR one-shot by calculating the specific counter for the offset.

        // Actually, for simplicity and performance in JS, let's implement the XOR keystream processing.
        // We will maintain `readCounter` (int, bytes processed).
        // 1. Calculate which block index we are at.
        // 2. Encrypt that counter-block using AES-ECB (simulated via 1-block AES-CTR with 0-IV? No).

        // Simplest consistent way with WebCrypto:
        // Use `encrypt` (yes, encrypt, CTR mode is symmetric) to generate the keystream for the specific range.
        // Then XOR manually.

        // To generate keystream for a chunk at byte-offset `N` with length `L`:
        // We need to encrypt a buffer of zeros that corresponds to that range... 
        // BUT WebCrypto AES-CTR takes the InitialCounter.
        // So for offset N, calculate new Counter = InitialIV + (N / 16).
        // Then Encrypt(Zeros, Counter) -> KeyStream.
        // Then Data XOR KeyStream.

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
        const alg = {
            name: "AES-CTR",
            counter: this.incrementIV(initialIV, Math.floor(byteOffset / 16)),
            length: 128
        }

        // We generate keystream.
        // To match byteOffset precisely (e.g. if we are in middle of a block),
        // we might need to decrypt a slightly larger/aligned buffer or shift result.
        // Using WebCrypto AES-CTR on the input directly handles the block framing relative to the PROVIDED counter.
        // Standard WebCrypto AES-CTR: The counter block is formatted as the provided 'counter' (16 bytes).
        // It processes the input.

        // PROBLEM: If byteOffset is not a multiple of 16, we are mid-block.
        // WebCrypto AES-CTR always starts at the beginning of the flag-block defined by 'counter'.
        // So if byteOffset = 1, we need to effectively start at byte 1 of the keystream.
        // We can align:
        const blockOffset = Math.floor(byteOffset / 16);
        const remainder = byteOffset % 16;

        // Prepare a buffer of zeros to generate keystream?
        // OR just pass the data?
        // If we pass data to `encrypt`, with counter aligned to block, 
        // but our data is actually starting at `remainder`, we have to be careful.
        // Data: [Byte 1, Byte 2...]
        // If we say counter is Block 0. Encrypting Data[0] will XOR with Keystream[0].
        // But we want Keystream[1].

        // Workaround: Prepad `remainder` dummy bytes to input, encrypt, then slice off.
        const alignedInput = new Uint8Array(remainder + input.length);
        alignedInput.set(input, remainder);

        alg.counter = this.incrementIV(initialIV, blockOffset);

        const ciphertext = await crypto.subtle.encrypt(alg, this.key, alignedInput);

        return new Uint8Array(ciphertext).slice(remainder);
    }

    incrementIV(iv, blockCount) {
        // Increment the 128-bit counter (iv) by blockCount
        // Treat as Big Endian integer
        // Use BigInt for simplicity
        const view = new DataView(iv.buffer, iv.byteOffset, iv.byteLength);
        let high = view.getBigUint64(0, false); // BigEndian
        let low = view.getBigUint64(8, false); // BigEndian

        // Handle carry
        // Javascript BigInt usually handles arbitrary precision, but we want 128-bit behavior.
        // If low overflows 64-bit, we need to correct high.
        // We can treat high/low as a single 128-bit value if we reconstruct.
        // Or manual carry.

        // Simpler: (High << 64) + Low + BlockCount
        let val = (high << 64n) | low;
        val += BigInt(blockCount);

        // Write back
        const newIV = new Uint8Array(16);
        const newView = new DataView(newIV.buffer);

        const newHigh = val >> 64n;
        const newLow = val & 0xFFFFFFFFFFFFFFFFn;

        newView.setBigUint64(0, newHigh, false);
        newView.setBigUint64(8, newLow, false);
        return newIV;
    }

    close() {
        this.stream.close();
    }

    // Event Emitter Implementation
    emit(event, ...args) {
        if (this.handlers && this.handlers[event]) {
            this.handlers[event].forEach(h => h(...args));
        }
    }

    on(event, handler) {
        if (!this.handlers) this.handlers = {};
        if (!this.handlers[event]) this.handlers[event] = [];
        this.handlers[event].push(handler);

        // Forward 'close' and 'error' from underlying stream if not already did
        if (event !== 'data' && !this.listeningToStreamErrors) {
            // We can't really "hook on demand" easily without duplication checks.
            // Easier to just hook once in constructor for close/error.
        }
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
