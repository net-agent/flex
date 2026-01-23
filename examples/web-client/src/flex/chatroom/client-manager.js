import { reactive } from 'vue';
import { ChatClient } from './client.js';
import { chatStorage } from '../../services/chat-storage.js';
import { FLEX_CHAT_PORT } from '../core/constants.js';

class ChatClientManager {
    constructor() {
        this.state = reactive({
            sessions: [], // Array of joined rooms (client connections)
            activeSessionId: null
        });
        this.node = null;
    }

    init(node) {
        this.node = node;
    }

    async restoreSessions() {
        // Auto-Load Persistence
        const rooms = await chatStorage.getAllRooms();
        for (const room of rooms) {
            // Restore session
            const nickname = this.node.domain || "User";

            // For simplicity in decoupled mode:
            // 1. We just try to join.
            // 2. If it was a 'Host' room, we assume ServerManager might be handling it, 
            //    BUT we just treat it as a loopback join to 'domain'.

            let targetDomain = null;

            if (room.isHost) {
                targetDomain = this.node.domain;
            } else {
                const match = room.id.match(/^room-(.+?):(\d+)$/);
                if (match) {
                    targetDomain = match[1];
                }
            }

            if (targetDomain) {
                console.log(`[ChatClientManager] Restoring session to ${targetDomain}`);
                await this.joinSession(room.id, targetDomain, nickname);
            }
        }
    }

    async joinSession(existingRoomId, domain, nickname) {
        const port = FLEX_CHAT_PORT;
        const client = new ChatClient(this.node);

        const connected = await client.connect(domain, port, nickname);
        if (!connected) return false;

        // Session Setup
        let session = null;

        // Define ID early if possible
        let roomId = existingRoomId; // Might be null if new join

        client.onRoomInfo = async (info) => {
            const finalRoomId = info.roomId || roomId || `room-${domain}:${port}`;
            roomId = finalRoomId; // Update closure variable

            // Check if exists
            let existingIdx = this.state.sessions.findIndex(s => s.id === finalRoomId);

            if (existingIdx === -1) {
                // Check DB
                let dbRoom = await chatStorage.getRoom(finalRoomId);
                const newSession = {
                    id: finalRoomId,
                    name: info.name || (dbRoom ? dbRoom.name : `Room ${finalRoomId.substr(0, 4)}`),
                    client: client,
                    messages: [],
                    users: [],
                    unread: 0,
                    lastSeqId: dbRoom ? dbRoom.lastSeqId : 0
                };
                this.state.sessions.push(newSession);

                // Save to DB
                if (!dbRoom) {
                    await chatStorage.saveRoom({
                        id: finalRoomId,
                        name: info.name,
                        isHost: (domain === this.node.domain), // Mark if we are technically the host (loopback)
                        lastSeqId: 0,
                        createdAt: Date.now()
                    });
                }
            } else {
                // Update
                this.state.sessions[existingIdx].name = info.name;
                this.state.sessions[existingIdx].client = client;
            }

            // Get Reactive Proxy
            session = this.state.sessions.find(s => s.id === finalRoomId);

            // Set Active if none
            if (!this.state.activeSessionId) {
                this.state.activeSessionId = finalRoomId;
            }

            // Load History
            const history = await chatStorage.getMessages(finalRoomId);
            session.messages.splice(0, session.messages.length, ...history);

            // Sync
            client.sendSyncRequest(session.lastSeqId);
        };

        client.onMessage = async (msg) => {
            if (!session) return;
            msg.isMine = msg.sender === nickname;

            if (!session.messages.find(m => m.id === msg.id)) {
                session.messages.push(msg);
            }
            if (this.state.activeSessionId !== session.id) {
                session.unread++;
            }
            // Persist
            if (msg.seqId > session.lastSeqId) {
                session.lastSeqId = msg.seqId;
                await chatStorage.saveRoom({
                    id: session.id,
                    name: session.name,
                    lastSeqId: session.lastSeqId
                });
            }
            await chatStorage.saveMessage(session.id, msg);
        };

        client.onSyncResp = async (resp) => {
            const s = this.state.sessions.find(s => s.client === client);
            if (!s) return;

            const newMsgs = resp.messages || [];
            if (newMsgs.length > 0) {
                const unique = newMsgs.filter(nm => !s.messages.some(em => em.id === nm.id));
                unique.forEach(m => {
                    m.isMine = m.sender === nickname;
                    s.messages.push(m);
                    chatStorage.saveMessage(s.id, m);
                });
                // Update seq
                const max = Math.max(...newMsgs.map(m => m.seqId || 0));
                if (max > s.lastSeqId) {
                    s.lastSeqId = max;
                    await chatStorage.saveRoom({ id: s.id, name: s.name, lastSeqId: max });
                }
                s.messages.sort((a, b) => a.seqId - b.seqId);
            }
        };

        client.onUserList = (users) => {
            const s = this.state.sessions.find(s => s.client === client);
            if (s) s.users = users;
        };

        return true;
    }

    async sendMessage(sessionId, text) {
        const session = this.state.sessions.find(s => s.id === sessionId);
        if (session && session.client) {
            await session.client.sendMessage(text);
        }
    }

    async leaveSession(sessionId) {
        const idx = this.state.sessions.findIndex(s => s.id === sessionId);
        if (idx !== -1) {
            const s = this.state.sessions[idx];
            if (s.client) s.client.disconnect();
            this.state.sessions.splice(idx, 1);
            if (this.state.activeSessionId === sessionId) {
                this.state.activeSessionId = this.state.sessions.length > 0 ? this.state.sessions[0].id : null;
            }
            await chatStorage.deleteRoom(sessionId);
        }
    }
}

export const chatClientManager = new ChatClientManager();
