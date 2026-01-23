// Test script for SecretListener (Refactored API)
import { ListenSecret } from './src/flex/core/secret_listener.js';
import { SecretStream } from './src/flex/core/secret_stream.js';
import { FlexNode } from './src/flex/core/node.js';

// Polyfill WebCrypto for Node if needed
import { webcrypto } from 'node:crypto';
if (!globalThis.crypto) {
    globalThis.crypto = webcrypto;
}

process.on('unhandledRejection', (reason, p) => {
    console.error('Unhandled Rejection at:', p, 'reason:', reason);
    process.exit(1);
});

class MockNode {
    constructor() {
        this.listenhub = {
            listen: (port, cb) => {
                console.log(`[MockNode] Listening on ${port} (Basic)`);
                this.callback = cb;
                return true;
            }
        };
        this.dialer = {
            dial: async (d, p) => {
                console.log(`[MockNode] Dialing ${d}:${p} (Basic)`);
                return new MockStream("ClientBasic");
            }
        };
    }

    onLog(msg) { console.log(msg); }

    // Copy the new logic from node.js
    async dial(domain, port, secret = "") {
        let stream = await this.dialer.dial(domain, port);

        if (secret && typeof secret === 'string' && secret.length > 0) {
            console.log(`[Secret] Securing connection to ${domain}:${port}...`);
            const ss = new SecretStream(stream, secret);
            try {
                await ss.init();
                return ss;
            } catch (err) {
                stream.close();
                throw err;
            }
        }
        return stream;
    }

    listen(port, secret, callback) {
        if (typeof secret === 'function') {
            callback = secret;
            secret = "";
        }

        if (secret && typeof secret === 'string' && secret.length > 0) {
            return ListenSecret(this, port, secret, callback);
        } else {
            return this.listenhub.listen(port, callback);
        }
    }
}

class MockStream {
    constructor(name = "Stream") {
        this.name = name;
        this.handlers = {};
        this.remoteIP = "127.0.0.1";
        this.remotePort = 12345;
        this.partner = null;
        this.buffer = [];
    }

    on(event, handler) {
        if (!this.handlers[event]) this.handlers[event] = [];
        this.handlers[event].push(handler);

        // Flush buffer if data listener added
        if (event === 'data' && this.buffer.length > 0) {
            const flush = () => {
                if (this.buffer.length > 0) {
                    console.log(`[${this.name}] Flushing ${this.buffer.length} packets...`);
                }
                while (this.buffer.length > 0) {
                    const data = this.buffer.shift();
                    this.emit('data', data);
                }
            };
            setTimeout(flush, 0);
        }
    }

    emit(event, ...args) {
        if (this.handlers[event] && this.handlers[event].length > 0) {
            this.handlers[event].forEach(h => h(...args));
        } else if (event === 'data') {
            console.log(`[${this.name}] Buffering packet size: ${args[0].byteLength}`);
            this.buffer.push(args[0]);
        }
    }

    async write(data) {
        // console.log(`[${this.name}] Writing ${data.byteLength} bytes`);
        setTimeout(() => {
            if (this.partner) {
                this.partner.emit('data', data);
            }
        }, 10);
    }

    close() {
        this.emit('close');
    }
}

async function test() {
    try {
        const node = new MockNode();
        const port = 8080;
        const secret = "test-secret-123";

        console.log("--> Starting Listener (Secure)");
        node.listen(port, secret, (sStream) => {
            console.log("--> [Server] Connection Accepted!");

            sStream.on('data', (data) => {
                const str = new TextDecoder().decode(data);
                console.log(`--> [Server] RX: "${str}"`);
                sStream.write("Echo: " + str);
            });
        });

        console.log("--> Simulating Connection");

        let serverStream;
        node.dialer.dial = async (d, p) => {
            console.log("[Test] Override Dial called");
            const clientSide = new MockStream("ClientSide");
            const serverSide = new MockStream("ServerSide");
            clientSide.partner = serverSide;
            serverSide.partner = clientSide;

            serverStream = serverSide;
            console.log("[Test] serverStream captured");
            return clientSide;
        };

        // Hook listen
        node.listenhub.listen = (p, cb) => {
            console.log(`[MockNode] Listen registered on ${p}`);
            node.emitConnection = (s) => cb(s);
            return true;
        }

        // Re-register listener
        node.listen(port, secret, (sStream) => {
            console.log("--> [Server] Callback triggered");
            sStream.on('data', (data) => {
                const str = new TextDecoder().decode(data);
                console.log(`--> [Server] RX: "${str}"`);
                sStream.write("Echo: " + str);
            });
        });

        console.log("--> [Client] Dialing...");
        const dialPromise = node.dial("localhost", port, secret);

        // Schedule server connect
        console.log("[Test] Scheduling server connect...");
        setTimeout(() => {
            console.log("[Test] Timeout fired. Connecting server...");
            if (serverStream) {
                node.emitConnection(serverStream);
            } else {
                console.error("[Test] ERROR: serverStream missing");
            }
        }, 50);

        const clientSS = await dialPromise;
        console.log("--> [Client] Dial Verified!");

        await clientSS.write("Hello Refactor!");

        // Wait for echo
        await new Promise((resolve, reject) => {
            const timer = setTimeout(() => reject(new Error("Echo timeout")), 2000);
            clientSS.on('data', (d) => {
                const txt = new TextDecoder().decode(d);
                console.log(`--> [Client] RX: "${txt}"`);
                if (txt.includes("Echo")) {
                    clearTimeout(timer);
                    resolve();
                }
            });
        });

        console.log("TEST PASSED");
        process.exit(0);

    } catch (e) {
        console.error("TEST FAILED:", e);
        process.exit(1);
    }
}

test();
