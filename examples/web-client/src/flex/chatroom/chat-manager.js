import { reactive } from 'vue';
import { ChatServer } from './server.js';
import { ChatClient } from './client.js';
import { chatStorage } from '../../services/chat-storage.js';
import { FLEX_CHAT_PORT } from '../core/constants.js';

class ChatManager {
    constructor() {
        // sessions: Array<{ id, name, type (host/client), client, server?, unread, lastMsg, lastSeqId }>
        // Note: 'id' in session state corresponds to 'roomId' in DB and Protocol
        this.state = reactive({
            sessions: [],
            activeSessionId: null
        });
        this.node = null;
    }

    async init(node) {
        this.node = node;

        // Auto-Load Persistence
        const rooms = await chatStorage.getAllRooms();
        let hasHostRoom = false;

        for (const room of rooms) {
            console.log(`[ChatManager] Restoring room: ${room.name} (${room.id})`);
            if (room.isHost) hasHostRoom = true;

            // Join session (restore)
            // Determine type and nickname context
            const nickname = this.node.domain || "User"; // Default identifier

            if (room.isHost) {
                // Restore Host Session
                // For host, we need to restart the server
                this.createHostSession(room.name, nickname, true); // true for 'restoring'
            } else {
                // Restore Client Session
                // We use roomId as target for now, but joinSession expects domain/port.
                // The room.id (e.g. `room-${domain}:${port}`) contains connection info usually.
                // We need to parse it or store connection info better.
                // Currently room ID logic is `room-${domain}:${port}`.

                // Let's parse ID if possible: custom format.
                // If we can't parse, we rely on the fact that `client.connect` needs args.
                // Maybe we stored them? Schema: id, name.
                // We might need to store `targetDomain` in room DB.
                // For now, let's assume standard format `room-domain:port`.

                const match = room.id.match(/^room-(.+?):(\d+)$/);
                if (match) {
                    const domain = match[1];
                    // const port = parseInt(match[2]); // Port hardcoded to FLEX_CHAT_PORT usually
                    await this.joinSession(room.id, domain, nickname);
                } else {
                    // Fallback or skip
                    console.warn(`[ChatManager] Could not parse room ID to restore connection: ${room.id}`);
                }
            }
        }

        // Auto-Host Default Room if none exists
        if (!hasHostRoom && this.node.domain) {
            console.log("[ChatManager] Auto-hosting default room");
            this.createHostSession(`${this.node.domain}'s chat room`, this.node.domain);
        }
    }

    createHostSession(name, nickname, isRestore = false) {
        const port = FLEX_CHAT_PORT;

        const server = new ChatServer(this.node);

        server.start(port, name).then(success => {
            if (!success) {
                // If restoring and failed (maybe port busy?), just log
                if (isRestore) console.warn("[ChatManager] Failed to restore host server");
                return;
            }

            const roomId = server.roomId;

            // Check if exists (might have been pushed by race condition or logic?)
            if (this.state.sessions.find(s => s.id === roomId)) return;

            const session = {
                id: roomId,
                name: name || `Host Room`,
                type: 'host',
                server: server,
                client: null,
                messages: [],
                users: [],
                unread: 0,
                lastSeqId: server.lastSeqId
            };

            this.state.sessions.push(session);

            // Join myself to my own server
            this.joinSession(roomId, this.node.domain, nickname);
        });

        return true;
    }

    async joinSession(sessionId, domain, nickname) {
        const port = FLEX_CHAT_PORT;

        const client = new ChatClient(this.node);

        // Connect first to get Room Info
        const connected = await client.connect(domain, port, nickname);
        if (!connected) return false;

        let session = null;

        // Callbacks Setup
        client.onRoomInfo = async (info) => {
            console.log("[ChatManager] RoomInfo:", info);

            // RoomID from server is the source of truth for DB storage
            const roomId = info.roomId || sessionId || `room-${domain}:${port}`;

            // Find or Create Session State (Reactive lookup from ROOT)
            const existingIdx = this.state.sessions.findIndex(s => s.id === roomId);

            if (existingIdx === -1) {
                // Check DB
                let dbRoom = await chatStorage.getRoom(roomId);

                const newSession = {
                    id: roomId,
                    name: info.name || (dbRoom ? dbRoom.name : `Room ${roomId.substr(0, 4)}`),
                    type: 'client',
                    server: null,
                    client: client,
                    messages: [],
                    users: [],
                    unread: 0,
                    lastSeqId: dbRoom ? dbRoom.lastSeqId : 0
                };
                this.state.sessions.push(newSession);

                // If new to DB, save it
                if (!dbRoom) {
                    await chatStorage.saveRoom({
                        id: roomId,
                        name: info.name,
                        isHost: false,
                        lastSeqId: 0,
                        createdAt: Date.now()
                    });
                }
            } else {
                // Update existing session
                const s = this.state.sessions[existingIdx];
                s.name = info.name;
                s.client = client;
            }

            // CRITICAL FIX: Ensure 'session' variable points to the REACTIVE PROXY
            session = this.state.sessions.find(s => s.id === roomId);

            // Set Active
            if (!this.state.activeSessionId) {
                this.state.activeSessionId = roomId;
            }

            // Load History from DB
            const history = await chatStorage.getMessages(roomId);
            console.log(`[ChatManager] Loaded ${history.length} msg from DB`);

            // Re-populate messages (ensure reactive trigger)
            session.messages.splice(0, session.messages.length, ...history);

            // Trigger Sync for new messages
            console.log(`[ChatManager] Sending SyncReq from ${session.lastSeqId}`);
            client.sendSyncRequest(session.lastSeqId);
        };

        client.onMessage = async (msg) => {
            // Guard: Session might not be initialized yet if onMessage fires before onRoomInfo (unlikely but possible)
            if (!session) {
                console.warn("Message received before session init");
                return;
            }

            console.log("[ChatManager] Msg:", msg);
            msg.isMine = msg.sender === nickname;

            // Dedup
            const exists = session.messages.find(m => m.id === msg.id);
            if (!exists) {
                session.messages.push(msg); // Mutate PROXY -> Triggers UI
            }

            if (this.state.activeSessionId !== session.id) {
                session.unread++;
            }

            // Update LastSeq & Persist
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
            if (!session) return;
            console.log("[ChatManager] Sync Packet:", resp);

            const newMsgs = resp.messages;
            if (newMsgs && newMsgs.length > 0) {
                // Filter duplicates
                const uniqueNew = newMsgs.filter(nm => !session.messages.some(em => em.id === nm.id));

                uniqueNew.forEach((m) => {
                    m.isMine = m.sender === nickname;
                    session.messages.push(m);
                    // Persist
                    chatStorage.saveMessage(session.id, m);
                });

                // Update SeqId
                const maxSeq = Math.max(...newMsgs.map(m => m.seqId || 0));
                if (maxSeq > session.lastSeqId) {
                    session.lastSeqId = maxSeq;
                    await chatStorage.saveRoom({
                        id: session.id,
                        name: session.name,
                        lastSeqId: maxSeq
                    });
                }

                // Sort
                session.messages.sort((a, b) => a.seqId - b.seqId);
            }
        };

        client.onUserList = (users) => {
            if (session) session.users = users;
        };

        return true;
    }

    async sendMessage(sessionId, text) {
        // Use reactive lookup for consistency
        const session = this.state.sessions.find(s => s.id === sessionId);
        if (!session || !session.client) return;

        const success = await session.client.sendMessage(text);
        if (!success) {
            console.error("Failed to send message");
        }
    }

    async leaveSession(sessionId) {
        const idx = this.state.sessions.findIndex(s => s.id === sessionId);
        if (idx === -1) return;

        const session = this.state.sessions[idx];
        if (session.client) session.client.disconnect();
        if (session.server) session.server.stop();

        this.state.sessions.splice(idx, 1);
        if (this.state.activeSessionId === sessionId) {
            this.state.activeSessionId = this.state.sessions.length > 0 ? this.state.sessions[0].id : null;
        }

        // Remove from Persistence
        await chatStorage.deleteRoom(sessionId);
    }

    getActiveSession() {
        return this.state.sessions.find(s => s.id === this.state.activeSessionId);
    }
}

export const chatManager = new ChatManager();
