import { reactive } from 'vue';
import { ChatServer } from './server.js';
import { FLEX_CHAT_PORT } from '../core/constants.js';
import { chatStorage } from '../../services/chat-storage.js';

class ChatServerManager {
    constructor() {
        this.state = reactive({
            isRunning: false,
            roomName: '',
            roomId: null,
            users: [] // Server-side user list (connected clients)
        });
        this.server = null;
        this.node = null;
    }

    init(node) {
        this.node = node;
    }

    async startServer(name, nickname) {
        if (this.server) return false;

        this.server = new ChatServer(this.node);
        const success = await this.server.start(FLEX_CHAT_PORT, name);

        if (success) {
            this.state.isRunning = true;
            this.state.roomName = name;
            this.state.roomId = this.server.roomId;

            // Persist the fact that we are hosting (so we can auto-start or recover)
            // Ideally we separate "My Room Config" from "Saved Session History".
            // For now, we can piggyback on chatStorage or add a preference.
            // Let's rely on the ClientManager's auto-join to trigger the server start? 
            // OR: We explicitly save "Host Config".

            // In the decoupled model, ServerManager should probably auto-start if configured.
        }
        return success;
    }

    stopServer() {
        if (this.server) {
            this.server.stop();
            this.server = null;
        }
        this.state.isRunning = false;
        this.state.users = [];
    }
}

export const chatServerManager = new ChatServerManager();
