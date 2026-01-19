export const CMD_ACK_FLAG = 1;
export const CMD_OPEN_STREAM = 2; // iota << 1
export const CMD_CLOSE_STREAM = 4;
export const CMD_PUSH_STREAM_DATA = 6;
export const CMD_PUSH_MESSAGE = 8;
export const CMD_PING_DOMAIN = 10;

// Protocol Header Size: 1 + 2 + 2 + 2 + 2 + 2 = 11 bytes
export const HEADER_SZ = 11;

export class Packet {
    constructor(cmd = 0) {
        this.header = new ArrayBuffer(HEADER_SZ);
        this.view = new DataView(this.header);
        this.payload = new ArrayBuffer(0); // Empty payload by default

        if (cmd > 0) {
            this.setCmd(cmd);
        }
    }

    //
    // Header Fields
    //

    setCmd(cmd) {
        this.view.setUint8(0, cmd);
    }

    getCmd() {
        return this.view.getUint8(0);
    }

    setDist(ip, port) {
        this.view.setUint16(1, ip);
        this.view.setUint16(3, port);
    }

    getDistIP() {
        return this.view.getUint16(1);
    }

    getDistPort() {
        return this.view.getUint16(3);
    }

    setSrc(ip, port) {
        this.view.setUint16(5, ip);
        this.view.setUint16(7, port);
    }

    getSrcIP() {
        return this.view.getUint16(5);
    }

    getSrcPort() {
        return this.view.getUint16(7);
    }

    setPayload(buffer) {
        this.payload = buffer;
        this.view.setUint16(9, buffer.byteLength);
    }

    getPayloadSize() {
        return this.view.getUint16(9);
    }

    //
    // Serialization
    //

    // Serialize to ArrayBuffer for sending over WS
    toBytes() {
        const totalLen = HEADER_SZ + this.payload.byteLength;
        const buf = new Uint8Array(totalLen);

        // Copy Header
        buf.set(new Uint8Array(this.header), 0);

        // Copy Payload
        if (this.payload.byteLength > 0) {
            buf.set(new Uint8Array(this.payload), HEADER_SZ);
        }

        return buf.buffer;
    }

    // Parse from ArrayBuffer received from WS
    static fromBytes(buffer) {
        if (buffer.byteLength < HEADER_SZ) {
            throw new Error("Packet too short");
        }

        const p = new Packet();

        // Copy Header
        const headerSlice = buffer.slice(0, HEADER_SZ);
        p.header = headerSlice;
        p.view = new DataView(p.header);

        // Check payload size
        const payloadSize = p.getPayloadSize();
        // Note: WS frames are message boundaries, so we can trust the frame length roughly,
        // but robust code ensures we have enough bytes.

        // Copy Payload
        if (payloadSize > 0) {
            // Ensure we don't read past buffer end
            const end = Math.min(HEADER_SZ + payloadSize, buffer.byteLength);
            p.payload = buffer.slice(HEADER_SZ, end);
        }

        return p;
    }
}
