export const IV_LEN = 16;
export const CHECKSUM_LEN = 16;
export const PACKET_CODE = 0x09;
export const SALT = "cipherconn-of-exchanger";

export class CipherUtils {
    /**
     * Increment a 128-bit counter (IV) by blockCount.
     * @param {Uint8Array} iv 
     * @param {number} blockCount 
     * @returns {Uint8Array} New IV
     */
    static incrementIV(iv, blockCount) {
        // Increment the 128-bit counter (iv) by blockCount
        // Treat as Big Endian integer
        // Use BigInt for simplicity
        const view = new DataView(iv.buffer, iv.byteOffset, iv.byteLength);
        let high = view.getBigUint64(0, false); // BigEndian
        let low = view.getBigUint64(8, false); // BigEndian

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

    /**
     * Derive AES-CTR Key using HKDF-SHA1.
     * @param {string} secret 
     * @returns {Promise<CryptoKey>}
     */
    static async deriveKey(secret) {
        const enc = new TextEncoder();
        const keyMaterial = await crypto.subtle.importKey(
            "raw",
            enc.encode(secret),
            { name: "HKDF" },
            false,
            ["deriveKey"]
        );

        const salt = enc.encode(SALT);
        const info = new Uint8Array(0); // empty info matching Go's nil

        return await crypto.subtle.deriveKey(
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
    }

    /**
     * Crypt (Encrypt/Decrypt) data using AES-CTR with manual counter alignment.
     * @param {CryptoKey} key 
     * @param {Uint8Array} initialIV 
     * @param {number} byteOffset 
     * @param {Uint8Array} input 
     * @returns {Promise<Uint8Array>}
     */
    static async crypt(key, initialIV, byteOffset, input) {
        const alg = {
            name: "AES-CTR",
            counter: null, // Set below
            length: 128
        };

        const blockOffset = Math.floor(byteOffset / 16);
        const remainder = byteOffset % 16;

        // Workaround: Prepad `remainder` dummy bytes to input, encrypt, then slice off.
        const alignedInput = new Uint8Array(remainder + input.length);
        alignedInput.set(input, remainder);

        alg.counter = this.incrementIV(initialIV, blockOffset);

        const ciphertext = await crypto.subtle.encrypt(alg, key, alignedInput);

        return new Uint8Array(ciphertext).slice(remainder);
    }
}
