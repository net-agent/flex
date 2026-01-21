import { Packet, CMD_OPEN_STREAM, ACK_OPEN_STREAM } from './packet.js';
import { Stream, StreamState } from './stream.js';

export class ListenHub {
    /**
     * @param {FlexNode} host 
     * @param {PortManager} portm 
     * @param {DataHub} datahub 
     */
    constructor(host, portm, datahub) {
        this.host = host;
        this.portm = portm;
        this.datahub = datahub;

        // map[port] -> callback(stream)
        this.listeners = new Map();
    }

    /**
     * Listen on a specific port.
     * @param {number} port 
     * @param {function(Stream)} callback 
     * @returns {boolean} success
     */
    listen(port, callback) {
        if (this.listeners.has(port)) {
            return false; // Already listening
        }

        try {
            this.portm.usePort(port);
            this.listeners.set(port, callback);
            return true;
        } catch (e) {
            return false;
        }
    }

    /**
     * Stop listening on a port.
     * @param {number} port 
     */
    close(port) {
        if (this.listeners.has(port)) {
            this.listeners.delete(port);
            this.portm.releasePort(port);
        }
    }

    /**
     * Handle incoming Open Stream Request.
     * @param {Packet} pbuf 
     */
    handleCmdOpenStream(pbuf) {
        // DistPort is our local port they want to connect to
        const localPort = pbuf.getDistPort();
        const listener = this.listeners.get(localPort);

        let errMsg = "";
        let stream = null;

        if (!listener) {
            errMsg = "connection refused";
        } else {
            // Create Stream
            // Request Packet: SrcIP=Remote, SrcPort=Remote, DistIP=Local, DistPort=Local

            // Resolve Remote Domain from payload
            const decoder = new TextDecoder();
            let remoteDomain = decoder.decode(pbuf.payload);
            if (!remoteDomain) {
                // Should resolve IP to domain if possible, or just use IP string
                remoteDomain = `${pbuf.getSrcIP()}`;
            }

            stream = new Stream(
                this.host,
                this.host.domain,
                pbuf.getSrcIP(),
                pbuf.getSrcPort(),
                remoteDomain,
                pbuf.getDistIP(),
                pbuf.getDistPort()
            );

            // Try attach
            try {
                this.datahub.attachStream(stream);
            } catch (e) {
                errMsg = e.message;
                stream = null;
            }
        }

        // Send ACK
        // ACK Packet: SrcIP=Local, SrcPort=Local, DistIP=Remote, DistPort=Remote
        const ack = new Packet(ACK_OPEN_STREAM);
        ack.setSrc(pbuf.getDistIP(), pbuf.getDistPort());
        ack.setDist(pbuf.getSrcIP(), pbuf.getSrcPort());

        if (errMsg) {
            const encoder = new TextEncoder();
            ack.setPayload(encoder.encode(errMsg));
        } else {
            ack.setPayload(null);
        }

        this.host.send(ack);

        // If success, trigger listener
        if (stream && !errMsg) {
            stream.state = StreamState.Establish;
            listener(stream);
        }
    }
}
