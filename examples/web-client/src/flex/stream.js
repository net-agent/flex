import { Packet, CMD_PUSH_STREAM_DATA, ACK_PUSH_STREAM_DATA, CMD_CLOSE_STREAM, ACK_CLOSE_STREAM } from './packet.js';

export const StreamState = {
    Closed: 0,
    Dialing: 1,
    Accepting: 2,
    Establish: 3,
    LocalClose: 4,
    RemoteClose: 5,
};

/**
 * Stream mimics a net.Conn.
 * Events: 'data', 'close', 'error'
 */
export class Stream {
    /**
     * @param {Node} host - The main FlexNode instance
     * @param {string} localDomain
     * @param {number} remoteIP
     * @param {number} remotePort
     * @param {string} remoteDomain
     * @param {number} localIP
     * @param {number} localPort
     */
    constructor(host, localDomain, remoteIP, remotePort, remoteDomain, localIP, localPort) {
        this.host = host;
        this.localDomain = localDomain;
        this.remoteIP = remoteIP;
        this.remotePort = remotePort;
        this.remoteDomain = remoteDomain;
        this.localIP = localIP;
        this.localPort = localPort;

        this.state = StreamState.Establish;
        this.readBuffer = [];
        this.handlers = {
            data: [],
            close: [],
            error: []
        };

        // Flow Control
        this.bucketSz = 2 * 1024 * 1024; // 2MB Initial Window
        this.writeQueue = []; // Array of { resolve, reject, data }
    }

    on(event, handler) {
        if (this.handlers[event]) {
            this.handlers[event].push(handler);
        }
    }

    emit(event, ...args) {
        if (this.handlers[event]) {
            this.handlers[event].forEach(h => h(...args));
        }
    }

    /**
     * Handle incoming data packet (CMD_PUSH_STREAM_DATA)
     * @param {Packet} pbuf 
     */
    handlePushData(pbuf) {
        if (this.state === StreamState.Closed || this.state === StreamState.RemoteClose) {
            // Send Reset/Ack? In Go we send Reset.
            // For now just ignore or log.
            return;
        }

        const payloadLen = pbuf.payload.byteLength;
        if (payloadLen > 0) {
            // Emit data event
            // Payload is Uint8Array or ArrayBuffer
            this.emit('data', pbuf.payload);
        }

        // Send ACK with consumed size
        // ACK_PUSH_STREAM_DATA (7)
        // Src/Dist swapped relative to incoming
        const ack = new Packet(ACK_PUSH_STREAM_DATA);
        ack.setDist(pbuf.getSrcIP(), pbuf.getSrcPort());
        ack.setSrc(pbuf.getDistIP(), pbuf.getDistPort());
        ack.setACKInfo(payloadLen);

        this.host.send(ack);
    }

    /**
     * Handle ACK for Push Data (ACK_PUSH_STREAM_DATA)
     * @param {Packet} pbuf
     */
    handleAckPushData(pbuf) {
        const n = pbuf.getACKInfo();
        this.bucketSz += n;
        this.processWriteQueue();
    }

    /**
     * Write data to the stream.
     * @param {Uint8Array|ArrayBuffer|string} data 
     * @returns {Promise<void>}
     */
    async write(data) {
        if (this.state !== StreamState.Establish && this.state !== StreamState.RemoteClose) {
            throw new Error("stream not connected");
        }

        let payload = data;
        if (typeof data === 'string') {
            const encoder = new TextEncoder();
            payload = encoder.encode(data);
        }

        // Ensure Uint8Array for slicing
        if (payload instanceof ArrayBuffer) {
            payload = new Uint8Array(payload);
        }

        // Push to queue and process
        return new Promise((resolve, reject) => {
            this.writeQueue.push({ resolve, reject, data: payload, offset: 0 });
            this.processWriteQueue();
        });
    }

    processWriteQueue() {
        while (this.writeQueue.length > 0) {
            const item = this.writeQueue[0];
            const remaining = item.data.length - item.offset;

            if (this.bucketSz <= 0) {
                // Blocked
                return;
            }

            // Calculate chunk size
            // Go uses DefaultSplitSize = 63KB
            const splitSize = 63 * 1024;
            let len = remaining;
            if (len > splitSize) len = splitSize;
            if (len > this.bucketSz) len = this.bucketSz;

            // Prepare Packet
            const chunk = item.data.slice(item.offset, item.offset + len);
            const p = new Packet(CMD_PUSH_STREAM_DATA);
            p.setSrc(this.localIP, this.localPort);
            p.setDist(this.remoteIP, this.remotePort);
            p.setPayload(chunk);

            this.host.send(p);

            // Update State
            this.bucketSz -= len;
            item.offset += len;

            if (item.offset >= item.data.length) {
                // Done with this write
                this.writeQueue.shift();
                item.resolve();
            } else {
                // Partially written, continue (will loop or return if bucket exhausted)
            }
        }
    }

    /**
     * Close the stream.
     */
    close() {
        if (this.state === StreamState.Closed || this.state === StreamState.LocalClose) {
            return;
        }

        this.state = StreamState.LocalClose;

        // Send Close CMD
        const p = new Packet(CMD_CLOSE_STREAM);
        p.setSrc(this.localIP, this.localPort);
        p.setDist(this.remoteIP, this.remotePort);
        p.setPayload(null);

        this.host.send(p);

        // Trigger local close event
        this.emit('close');
    }

    /**
     * Handle incoming close command (CMD_CLOSE_STREAM)
     */
    handleClose() {
        this.state = StreamState.RemoteClose;

        // Send Close ACK
        const p = new Packet(ACK_CLOSE_STREAM);
        p.setSrc(this.localIP, this.localPort);
        p.setDist(this.remoteIP, this.remotePort);
        p.setPayload(null);
        this.host.send(p);

        // Finally close locally
        this.state = StreamState.Closed;
        this.emit('close');
    }

    // Setters/Getters
    setRemoteDomain(domain) {
        this.remoteDomain = domain;
    }
}
