import { Packet, CMD_OPEN_STREAM, SWITCHER_IP } from './packet.js';
import { Stream } from './stream.js';
import { StreamState } from './stream.js';

export class Dialer {
    /**
     * @param {FlexNode} host 
     * @param {PortManager} portm 
     * @param {DataHub} datahub 
     */
    constructor(host, portm, datahub) {
        this.host = host;
        this.portm = portm;
        this.datahub = datahub;

        // Requests map to handle async open responses
        // key: localPort, value: { resolve, reject, timer }
        this.pendingDials = new Map();

        this.timeout = 5000; // 5s timeout
    }

    /**
     * Dial a domain.
     * @param {string} domain 
     * @param {number} port 
     * @returns {Promise<Stream>}
     */
    async dial(domain, port) {
        // 1. Alloc Port
        const srcPort = this.portm.getFreePort();

        // 2. Register pending request
        return new Promise((resolve, reject) => {
            const timer = setTimeout(() => {
                this.pendingDials.delete(srcPort);
                this.portm.releasePort(srcPort);
                reject(new Error("dial timeout"));
            }, this.timeout);

            this.pendingDials.set(srcPort, {
                resolve,
                reject,
                timer,
                domain,
                port,
                srcPort
            });

            // 3. Send Open Stream Packet
            try {
                const p = new Packet(CMD_OPEN_STREAM);
                p.setSrc(this.host.ip, srcPort);
                p.setDist(SWITCHER_IP, port); // Wait, if dialing domain, distIP is switcher usually? Or resolved IP?
                // In Flex v2, DialDomain sends payload=domain, DistIP=SwitcherIP(0xffff/0x0001?), DistPort=TargetPort
                // The switcher routes it. 
                // Wait, looking at Go code:
                // pbuf.SetDist(vars.SwitcherIP, port) where SwitcherIP is 1.
                // pbuf.SetPayload([]byte(domain))

                p.setDist(SWITCHER_IP, port);

                const encoder = new TextEncoder();
                p.setPayload(encoder.encode(domain));

                this.host.send(p);
            } catch (e) {
                clearTimeout(timer);
                this.pendingDials.delete(srcPort);
                this.portm.releasePort(srcPort);
                reject(e);
            }
        });
    }

    /**
     * Handle ACK for Open Stream
     * @param {Packet} pbuf 
     */
    handleAckOpenStream(pbuf) {
        const localPort = pbuf.getDistPort(); // Packet sent back to us
        const pending = this.pendingDials.get(localPort);

        if (!pending) {
            // Maybe timed out or unknown
            return;
        }

        // Clear timeout and map entry
        clearTimeout(pending.timer);
        this.pendingDials.delete(localPort);

        // Check for error in payload
        const decoder = new TextDecoder();
        const msg = decoder.decode(pbuf.payload);
        if (msg) {
            this.portm.releasePort(localPort);
            pending.reject(new Error(msg));
            return;
        }

        // Success! Create Stream
        // Response Pkt: SrcIP=RemoteIP, SrcPort=RemotePort, DistIP=LocalIP, DistPort=LocalPort
        const s = new Stream(
            this.host,
            this.host.domain,
            pbuf.getSrcIP(),
            pbuf.getSrcPort(),
            pending.domain, // Remote Domain
            pbuf.getDistIP(),
            pbuf.getDistPort()
        );
        s.state = StreamState.Establish;

        // Attach to DataHub
        try {
            this.datahub.attachStream(s);
            pending.resolve(s);
        } catch (e) {
            this.portm.releasePort(localPort);
            pending.reject(e);
        }
    }
}
