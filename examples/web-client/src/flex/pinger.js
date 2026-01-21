import { Packet, CMD_PING_DOMAIN, CMD_ACK_FLAG, SWITCHER_IP } from './packet.js';

export class Pinger {
    constructor(node) {
        this.node = node;
        this.nextSrcPort = 1000; // Start from 1000 to avoid conflicts
        this.pendingPings = new Map(); // port -> { resolve, reject, timer }
    }

    // PingDomain pings the target domain and returns the RTT in ms.
    // logical equivalent to Pinger.PingDomain in Go
    async pingDomain(domain, timeout = 5000) {
        const port = this.allocPort();

        return new Promise((resolve, reject) => {
            const timer = setTimeout(() => {
                this.cleanup(port);
                reject(new Error("ping domain timeout"));
            }, timeout);

            this.pendingPings.set(port, {
                resolve,
                reject,
                timer,
                start: Date.now()
            });

            this.sendPing(port, domain);
        });
    }

    allocPort() {
        // Simple round-robin port allocation
        // In a real implementation, we should check for collisions in pendingPings
        let port = this.nextSrcPort++;
        if (this.nextSrcPort > 0xFFFF) {
            this.nextSrcPort = 1000;
        }
        while (this.pendingPings.has(port)) {
            port = this.nextSrcPort++;
            if (this.nextSrcPort > 0xFFFF) {
                this.nextSrcPort = 1000;
            }
        }
        return port;
    }

    sendPing(port, domain) {
        const p = new Packet(CMD_PING_DOMAIN);
        p.setSrc(this.node.ip, port);
        p.setDist(SWITCHER_IP, 0);

        const encoder = new TextEncoder();
        p.setPayload(encoder.encode(domain));

        this.node.send(p);
    }

    handleRequest(packet) {
        const payloadStr = new TextDecoder().decode(packet.payload);
        const srcIP = packet.getSrcIP();
        const srcPort = packet.getSrcPort();

        let srcDomain = `ip=${srcIP}`;
        if (srcIP === SWITCHER_IP) {
            srcDomain = 'SWITCHER';
        }

        this.node.onLog(`Ping Request from '${srcDomain}:${srcPort}' (payload='${payloadStr}')`);

        // Prepare Response
        const resp = new Packet(CMD_PING_DOMAIN | CMD_ACK_FLAG);

        // Checking domain matching
        if (payloadStr !== this.node.domain) {
            const enc = new TextEncoder();
            resp.setPayload(enc.encode("domain not match"));
        } else {
            resp.setPayload(new ArrayBuffer(0));
        }

        // Swap Src/Dist
        // The incoming packet has Src=Sender, Dist=Me
        // Response should have Src=Me, Dist=Sender
        resp.setDist(packet.getSrcIP(), packet.getSrcPort());
        resp.setSrc(this.node.ip, 0); // SrcPort 0 for pings usually? Go impl uses 0 for Ack.

        this.node.send(resp);
    }

    handleAck(packet) {
        const port = packet.getDistPort();
        const entry = this.pendingPings.get(port);
        if (!entry) {
            return; // Unexpected or timed out ping ack
        }

        this.cleanup(port);

        const payload = new TextDecoder().decode(packet.payload);
        if (payload) {
            entry.reject(new Error(payload));
        } else {
            const rtt = Date.now() - entry.start;
            entry.resolve(rtt);
        }
    }

    cleanup(port) {
        const entry = this.pendingPings.get(port);
        if (entry) {
            clearTimeout(entry.timer);
            this.pendingPings.delete(port);
        }
    }
}
