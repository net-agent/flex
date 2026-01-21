import { Packet, CMD_PUSH_MESSAGE, CMD_CLOSE_STREAM, ACK_CLOSE_STREAM, ACK_PUSH_MESSAGE } from './packet.js';

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
     * Handle incoming data packet (CMD_PUSH_MESSAGE)
     * @param {Packet} pbuf 
     */
    handlePushData(pbuf) {
        if (this.state === StreamState.Closed || this.state === StreamState.RemoteClose) {
            // Send Reset/Ack? In Go we send Reset.
            // For now just ignore or log.
            return;
        }

        // Emit data event
        // Payload is Uint8Array or ArrayBuffer
        this.emit('data', pbuf.payload);

        // Send ACK
        // ACK_PUSH_MESSAGE
        // Use src/dist of incoming packet to reply? 
        // Or strictly use stored remoteIP/remotePort?
        // Packet swap src/dist logic:
        // Pbuf: SrcIP=Remote, Dist=Local
        // Reply: SrcIP=Local, Dist=Remote

        // Construct ACK
        const ack = new Packet(ACK_PUSH_MESSAGE);
        ack.setDist(pbuf.getSrcIP(), pbuf.getSrcPort());
        ack.setSrc(pbuf.getDistIP(), pbuf.getDistPort()); // which is us
        ack.setPayload(null); // Empty payload for ACK

        // IMPORTANT: ACK SID must match the incoming SID if SID is part of header?
        // In Flex v2, SID is implicitly (DistIP, DistPort, SrcIP, SrcPort).
        // But wait, the ACK message helps flow control? 
        // For simple reliability, we just ACK receipt.

        this.host.send(ack);
    }

    /**
     * Write data to the stream.
     * @param {Uint8Array|ArrayBuffer|string} data 
     */
    write(data) {
        if (this.state !== StreamState.Establish && this.state !== StreamState.RemoteClose) {
            throw new Error("stream not connected");
        }

        let payload = data;
        if (typeof data === 'string') {
            const encoder = new TextEncoder();
            payload = encoder.encode(data);
        }

        const p = new Packet(CMD_PUSH_MESSAGE);
        p.setSrc(this.localIP, this.localPort);
        p.setDist(this.remoteIP, this.remotePort);
        p.setPayload(payload);

        this.host.send(p);
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
