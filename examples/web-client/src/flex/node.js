import { Packet, CMD_OPEN_STREAM, CMD_PING_DOMAIN, CMD_ACK_FLAG, CMD_PUSH_MESSAGE } from './packet.js';

const PACKET_VERSION = 20221204;

export class FlexNode {
    constructor() {
        this.ws = null;
        this.ip = 0;
        this.domain = "";

        // State callbacks
        this.onLog = (msg) => console.log("[FlexNode]", msg);
        this.onStateChange = (state) => { }; // 'disconnected', 'connecting', 'handshaking', 'connected', 'ready'
        this.onLatencyUpdate = (domain, ms) => { };

        // Heartbeat
        this.heartbeatInterval = null;
        this.HEARTBEAT_PERIOD = 15000; // 15s

        // Ping tracking
        this.pingMap = new Map(); // port -> startTime
        this.pingIndex = 0;
    }

    connect(url, password) {
        if (this.ws) {
            this.ws.close();
        }

        this.changeState('connecting');
        this.onLog(`Connecting to ${url}...`);

        this.ws = new WebSocket(url);
        this.ws.binaryType = "arraybuffer";

        this.ws.onopen = () => {
            this.onLog("WebSocket Connected. Sending Handshake...");
            this.changeState('handshaking');
            this.sendHandshake(password);
        };

        this.ws.onmessage = (event) => {
            try {
                const p = Packet.fromBytes(event.data);
                this.handlePacket(p);
            } catch (e) {
                this.onLog(`Error parsing packet: ${e}`);
            }
        };

        this.ws.onerror = (e) => {
            this.onLog(`WS Error`);
        };

        this.ws.onclose = () => {
            this.onLog("WS Closed");
            this.changeState('disconnected');
            this.stopHeartbeat();
            this.ip = 0;
        };
    }

    disconnect() {
        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }
    }

    changeState(newState) {
        // Simple state machine
        this.state = newState;
        if (this.onStateChange) this.onStateChange(newState);
    }

    async sendHandshake(password) {
        // Generate random domain suffix or just use a random string for domain if not specified
        // For web client, let's generate a random ID
        const mac = "web-" + Math.random().toString(36).substr(2, 6);
        const domain = "web-" + Math.random().toString(36).substr(2, 6);
        this.domain = domain;
        const timestamp = Date.now() * 1000000; // Go uses UnixNano, let's try to match or use Unix*1000? 
        // Go: time.Now().UnixNano()

        const sum = await this.calcSum(domain, mac, password, timestamp);

        const req = {
            Version: PACKET_VERSION,
            Domain: domain,
            Mac: mac,
            Timestamp: timestamp,
            Sum: sum
        };

        const jsonStr = JSON.stringify(req);
        const encoder = new TextEncoder();
        const payload = encoder.encode(jsonStr);

        const p = new Packet(0); // Cmd 0 for upgrade/handshake
        p.setPayload(payload);

        this.send(p);
    }

    async calcSum(domain, mac, password, timestamp) {
        // Go: sha256 new, write string...
        // fmt.Sprintf("CalcSumStart,%v,%v,%v,%v,CalcSumEnd", req.Domain, req.Mac, password, req.Timestamp)
        const str = `CalcSumStart,${domain},${mac},${password},${timestamp},CalcSumEnd`;
        const encoder = new TextEncoder();
        const data = encoder.encode(str);
        const hashBuffer = await crypto.subtle.digest('SHA-256', data);

        return this.arrayBufferToBase64(hashBuffer);
    }

    arrayBufferToBase64(buffer) {
        let binary = '';
        const bytes = new Uint8Array(buffer);
        const len = bytes.byteLength;
        for (let i = 0; i < len; i++) {
            binary += String.fromCharCode(bytes[i]);
        }
        return window.btoa(binary);
    }

    startHeartbeat() {
        this.stopHeartbeat();
        this.heartbeatInterval = setInterval(() => {
            if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                this.ping("keepalive", true);
            }
        }, this.HEARTBEAT_PERIOD);
    }

    stopHeartbeat() {
        if (this.heartbeatInterval) {
            clearInterval(this.heartbeatInterval);
            this.heartbeatInterval = null;
        }
    }

    handlePacket(p) {
        // Handshake Response
        if (this.state === 'handshaking') {
            const decoder = new TextDecoder();
            const jsonStr = decoder.decode(p.payload);
            try {
                const resp = JSON.parse(jsonStr);
                if (resp.ErrCode === 0) {
                    this.ip = resp.IP;
                    this.onLog(`Handshake Success. IP=${this.ip}, Ver=${resp.Version}`);
                    this.changeState('ready');
                    this.startHeartbeat();
                } else {
                    this.onLog(`Handshake Failed: ${resp.ErrMsg}`);
                    this.disconnect();
                }
            } catch (e) {
                this.onLog(`Handshake Parse Error: ${e}`);
                this.disconnect();
            }
            return;
        }

        const cmd = p.getCmd();
        const distIP = p.getDistIP();
        const isAck = (cmd & CMD_ACK_FLAG) > 0;
        const rawCmd = cmd & ~CMD_ACK_FLAG;

        // Handle Ping Ack (Latency measurement)
        if (rawCmd === CMD_PING_DOMAIN && isAck) {
            const pingId = p.getDistPort();
            if (this.pingMap.has(pingId)) {
                const start = this.pingMap.get(pingId);
                const latency = Date.now() - start;
                this.pingMap.delete(pingId);

                const domain = new TextDecoder().decode(p.payload);
                this.onLog(`Ping Reply from '${domain || 'unknown'}': time=${latency}ms`);
                if (this.onLatencyUpdate) this.onLatencyUpdate(domain, latency);
            }
            return;
        }
    }

    ping(domain, isKeepalive = false) {
        if (!isKeepalive) this.onLog(`Pinging ${domain}...`);

        // Use existing connection logic
        const p = new Packet(CMD_PING_DOMAIN);
        const pingId = this.nextPingId();
        p.setSrc(this.ip, pingId);
        p.setDist(0, 0);

        const encoder = new TextEncoder();
        p.setPayload(encoder.encode(domain));

        this.pingMap.set(pingId, Date.now());
        this.send(p);
    }

    nextPingId() {
        this.pingIndex = (this.pingIndex + 1) % 0xFFFF;
        if (this.pingIndex === 0) this.pingIndex = 1;
        return this.pingIndex;
    }

    send(packet) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(packet.toBytes());
        }
    }
}
