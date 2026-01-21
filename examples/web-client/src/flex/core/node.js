import { Packet, CMD_OPEN_STREAM, ACK_OPEN_STREAM, CMD_PING_DOMAIN, CMD_ACK_FLAG, CMD_PUSH_MESSAGE, ACK_PUSH_MESSAGE, CMD_CLOSE_STREAM, ACK_CLOSE_STREAM, CMD_PUSH_STREAM_DATA, ACK_PUSH_STREAM_DATA, SWITCHER_IP } from './packet.js';
import { Pinger } from './pinger.js';
import { PortManager } from './port_manager.js';
import { DataHub } from './datahub.js';
import { Dialer } from './dialer.js';
import { ListenHub } from './listenhub.js';

const PACKET_VERSION = 20221204;

export class FlexNode {
    constructor(domain = "") {
        this.ws = null;
        this.ip = 0;
        this.domain = domain;

        // Core Components
        this.portm = new PortManager();
        this.datahub = new DataHub(this.portm);
        this.dialer = new Dialer(this, this.portm, this.datahub);
        this.listenhub = new ListenHub(this, this.portm, this.datahub);

        this.pinger = new Pinger(this);

        // State callbacks
        this.onLog = (msg) => console.log("[FlexNode]", msg);
        this.onStateChange = (state) => { };
        this.onDomainChange = (domain) => { };

        // Debug/Monitor
        this.monitorCallback = null; // (direction, packet) => {}

        // Heartbeat
        this.heartbeatInterval = null;
        this.HEARTBEAT_PERIOD = 15000;
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
        this.state = newState;
        if (this.onStateChange) this.onStateChange(newState);
    }

    async sendHandshake(password) {
        const mac = "web-" + Math.random().toString(36).substr(2, 6);

        let domain = this.domain;
        if (!domain) {
            domain = "web-" + Math.random().toString(36).substr(2, 6);
            this.domain = domain;
            if (this.onDomainChange) this.onDomainChange(domain);
        }

        const timestamp = Date.now() * 1000000;
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

        const p = new Packet(0);
        p.setPayload(payload);

        this.send(p);
    }

    async calcSum(domain, mac, password, timestamp) {
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
                this.pinger.pingDomain("keepalive", 5000).catch(e => { });
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
        if (this.monitorCallback) {
            this.monitorCallback('rx', p); // RX = Receive
        }

        // Handshake Response
        if (this.state === 'handshaking') {
            this.handleHandshakeResponse(p);
            return;
        }

        const cmd = p.getCmd();
        const isAck = (cmd & CMD_ACK_FLAG) > 0;
        const rawCmd = cmd & ~CMD_ACK_FLAG;

        // Route Packet
        switch (rawCmd) {
            case CMD_PING_DOMAIN:
                if (isAck) this.pinger.handleAck(p);
                else this.pinger.handleRequest(p);
                break;

            case CMD_OPEN_STREAM:
                if (isAck) this.dialer.handleAckOpenStream(p);
                else this.listenhub.handleCmdOpenStream(p);
                break;

            case CMD_PUSH_STREAM_DATA:
                if (!isAck) {
                    this.datahub.handlePushData(p);
                } else {
                    this.datahub.handleAckPushData(p);
                }
                break;

            case CMD_PUSH_MESSAGE:
                // Deprecated or different use?
                // For now, if we receive it, ignore or log
                break;

            case CMD_CLOSE_STREAM:
                if (!isAck) {
                    this.datahub.handleCloseStream(p);
                }
                break;

            default:
                // console.warn("Unknown CMD:", cmd);
                break;
        }
    }

    handleHandshakeResponse(p) {
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
    }

    // Public API
    async dial(domain, port) {
        return this.dialer.dial(domain, port);
    }

    listen(port, callback) {
        return this.listenhub.listen(port, callback);
    }

    send(packet) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            if (this.monitorCallback) {
                this.monitorCallback('tx', packet); // TX = Transmit
            }
            this.ws.send(packet.toBytes());
        }
    }
}

