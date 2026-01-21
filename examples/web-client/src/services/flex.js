import { reactive, ref } from 'vue';
import { FlexNode } from '../flex/core/node.js';

class FlexService {
    constructor() {
        this.node = new FlexNode();

        // Reactive State
        this.state = reactive({
            connectionState: 'disconnected', // disconnected, connecting, handshaking, ready
            ip: 0,
            domain: '',
            logs: [],
            connectedSince: null,
            theme: localStorage.getItem('flex_theme') || 'dark', // 'light' | 'dark'
        });

        // Bind Node Callbacks
        this.node.onLog = (msg) => {
            this.addLog(msg);
        };

        this.node.onStateChange = (newState) => {
            this.state.connectionState = newState;
            if (newState === 'ready') {
                this.state.ip = this.node.ip;
                this.state.domain = this.node.domain;
                this.state.connectedSince = Date.now();
            } else if (newState === 'disconnected') {
                this.state.ip = 0;
                this.state.connectedSince = null;
            }
        };

        this.node.onDomainChange = (domain) => {
            this.state.domain = domain;
        };
    }

    addLog(msg) {
        const time = new Date().toLocaleTimeString();
        // Append to bottom
        this.state.logs.push(`[${time}] ${msg}`);
        if (this.state.logs.length > 500) {
            this.state.logs.shift(); // Remove from top
        }
    }

    toggleTheme() {
        this.state.theme = this.state.theme === 'dark' ? 'light' : 'dark';
        localStorage.setItem('flex_theme', this.state.theme);
        // Apply class to body or html usually handled in App.vue or main.js, 
        // but let's expose the state here.
    }

    connect(url, password, domain) {
        if (domain) {
            this.node.domain = domain;
            this.state.domain = domain;
        }
        this.node.connect(url, password);
    }

    disconnect() {
        this.node.disconnect();
    }

    async ping(domain) {
        return this.node.pinger.pingDomain(domain, 5000);
    }
}

export const flexService = new FlexService();
