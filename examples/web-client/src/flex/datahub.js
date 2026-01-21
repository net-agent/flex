import { Packet } from './packet.js';

export class DataHub {
    /**
     * @param {PortManager} portm 
     */
    constructor(portm) {
        this.portm = portm;
        // map[sid] -> Stream
        // SID logic: In Go version, SID uses composite key or explicit ID.
        // JS Client simplified: 
        // Identify stream by LocalPort for now? 
        // Wait, a single port (like Listener) can have multiple streams (streams from diff remote IPs/Ports).
        // So Map key should be hash(LocalPort, RemoteIP, RemotePort).
        // BUT, packet routing relies on DistPort (LocalPort). 
        // If it's a listener port, we need SrcIP/SrcPort to demux.
        // If it's a dialer port (client), usually only one stream per ephemeral port.

        // Let's use string key: `${localPort}-${remoteIP}-${remotePort}`
        this.streams = new Map();
    }

    /**
     * Create a key for the stream map.
     */
    makeKey(localPort, remoteIP, remotePort) {
        return `${localPort}-${remoteIP}-${remotePort}`;
    }

    attachStream(stream) {
        const key = this.makeKey(stream.localPort, stream.remoteIP, stream.remotePort);
        if (this.streams.has(key)) {
            throw new Error("stream already exists");
        }
        this.streams.set(key, stream);

        // Auto clean on close
        stream.on('close', () => {
            if (this.streams.has(key)) {
                this.streams.delete(key);
                // Also release port if it was ephemeral (handled by Dialer/ListenHub?)
                // Actually, PortManager usage is tricky.
                // Dialer allocates ephemeral port: Release when stream closes.
                // Listener uses fixed port: Do NOT release when stream closes (only when listener closes).
            }
        });
    }

    getStream(localPort, remoteIP, remotePort) {
        return this.streams.get(this.makeKey(localPort, remoteIP, remotePort));
    }

    handlePushData(pbuf) {
        // DistPort is LocalPort
        const s = this.getStream(pbuf.getDistPort(), pbuf.getSrcIP(), pbuf.getSrcPort());
        if (s) {
            s.handlePushData(pbuf);
        } else {
            console.warn(`[DataHub] Stream not found for port ${pbuf.getDistPort()}`);
            // Send Reset/Close? 
        }
    }

    handleCloseStream(pbuf) {
        const s = this.getStream(pbuf.getDistPort(), pbuf.getSrcIP(), pbuf.getSrcPort());
        if (s) {
            s.handleClose();
        }
    }
}
