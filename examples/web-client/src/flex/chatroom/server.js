import { chatStorage } from '../../services/chat-storage.js';
import { FLEX_CHAT_PORT } from '../core/constants.js';

export class ChatServer {
    constructor(node) {
        this.node = node;
        this.port = 0;
        this.clients = new Map(); // Map<Stream, { nickname: string }>
        this.roomId = 'my-room'; // Default ID for now, in real app this would be dynamic
        this.maxHistory = 50;
        this.lastSeqId = 0;
    }

    async start(port = FLEX_CHAT_PORT, roomName = "Chat Room") {
        if (this.port !== 0) {
            console.warn("ChatServer already running");
            return false;
        }
        this.roomName = roomName;

        // 1. Persistence Init
        try {
            // Ensure room exists in DB
            const existing = await chatStorage.getRoom(this.roomId);
            if (existing) {
                // Restore seqId from DB state OR check actual messages
                // Ideally lastSeqId is stored in room metadata
                this.lastSeqId = existing.lastSeqId || 0;
                console.log(`[ChatServer] Restored room. LastSeqId: ${this.lastSeqId}`);
            } else {
                await chatStorage.saveRoom({
                    id: this.roomId,
                    name: this.roomName,
                    isHost: true,
                    lastSeqId: 0,
                    createdAt: Date.now()
                });
                this.lastSeqId = 0;
            }
        } catch (e) {
            console.error('[ChatServer] Storage Init Error:', e);
        }

        const success = this.node.listen(port, (stream) => {
            this.handleConnection(stream);
        });

        if (success) {
            this.port = port;
            console.log(`[ChatServer] Listening on port ${port}`);
        } else {
            console.error(`[ChatServer] Failed to listen on port ${port}`);
        }
        return success;
    }

    stop() {
        // Implement stop logic if exposed
        this.port = 0;
        this.clients.clear();
    }

    handleConnection(stream) {
        console.log(`[ChatServer] New connection from ${stream.remoteDomain}`);
        this.clients.set(stream, { nickname: `User-${stream.remoteDomain ? stream.remoteDomain.substr(0, 4) : '????'}` });

        stream.on('data', async (chunk) => {
            const text = new TextDecoder().decode(chunk);
            try {
                const msg = JSON.parse(text);

                if (msg.type === 'sync_req') {
                    await this.handleSyncRequest(stream, msg);
                } else {
                    await this.handleMessage(stream, msg);
                }
            } catch (e) {
                console.error("[ChatServer] Invalid JSON:", text, e);
            }
        });

        stream.on('close', () => {
            console.log(`[ChatServer] Client disconnected`);
            this.clients.delete(stream);
            this.broadcastUserList();
        });

        // Send Room Info (Immediate handshake)
        this.sendTo(stream, {
            type: 'room_info',
            name: this.roomName,
            roomId: this.roomId, // Send ID so client knows which DB to use
            timestamp: Date.now()
        });

        // Welcome message (Standard)
        // this.sendTo(stream, {
        //     type: 'sys',
        //     content: 'Welcome to the Flex Chat Room!',
        //     timestamp: Date.now()
        // });

        // Initial user list
        this.broadcastUserList();

        // Note: Historic messages are now pulled via 'sync_req' by the client,
        // so we DON'T dump history here automatically anymore.
    }

    async handleSyncRequest(stream, req) {
        const clientLastSeq = req.lastSeqId || 0;
        console.log(`[ChatServer] Sync Req from ${clientLastSeq} (Server Last: ${this.lastSeqId})`);

        try {
            // Get messages > clientLastSeq
            const missedMessages = await chatStorage.getMessages(this.roomId, clientLastSeq);
            console.log(`[ChatServer] Sending ${missedMessages.length} missed messages`);

            const resp = {
                type: 'sync_resp',
                messages: missedMessages,
                serverLastSeqId: this.lastSeqId
            };
            this.sendTo(stream, resp);
        } catch (e) {
            console.error('[ChatServer] Sync Error:', e);
        }
    }

    async persistMessage(msg) {
        try {
            await chatStorage.saveMessage(this.roomId, msg);
            await chatStorage.saveRoom({
                id: this.roomId,
                name: this.roomName,
                isHost: true,
                lastSeqId: this.lastSeqId
            });
        } catch (e) {
            console.error('[ChatServer] Save Error:', e);
        }
    }

    async handleMessage(senderStream, msg) {
        // console.log("[ChatServer] HandleMsg:", msg);
        if (!msg) return;

        if (msg.type === 'join') {
            const info = this.clients.get(senderStream);
            if (info) {
                info.nickname = msg.nickname || info.nickname;
                this.broadcastUserList();

                // System Message with ID and Sequence
                this.lastSeqId++;
                const sysMsg = {
                    type: 'sys',
                    id: crypto.randomUUID(),
                    roomId: this.roomId,
                    seqId: this.lastSeqId,
                    content: `${info.nickname} joined the chat`,
                    timestamp: Date.now()
                };

                // Persist
                await this.persistMessage(sysMsg);

                // Broadcast
                this.broadcast(sysMsg);
            }
            return;
        }

        if (msg.type === 'msg') {
            const info = this.clients.get(senderStream);
            const nickname = info ? info.nickname : 'Anonymous';

            // 1. Assign Sequence ID
            this.lastSeqId++;
            const seqMsg = {
                type: 'msg',
                id: crypto.randomUUID(),
                roomId: this.roomId,
                seqId: this.lastSeqId,
                sender: nickname,
                content: msg.content,
                timestamp: msg.timestamp || Date.now(),
                isHost: (senderStream.remoteDomain === this.node.domain) // Tag host messages
            };

            // 2. Persist
            await this.persistMessage(seqMsg);

            // 3. Broadcast to ALL (Ensuring consistency)
            // We broadcast back to sender too so they get the `seqId` and `id` confirmed.
            // Client side needs to handle dedup if they did optimistic UI.
            this.broadcast(seqMsg);
        }
    }

    broadcastUserList() {
        // const users = Array.from(this.clients.values()).map(c => c.nickname);
        const users = [];
        for (const [s, info] of this.clients.entries()) {
            users.push(info.nickname);
        }

        const packet = {
            type: 'user_list',
            users: users,
            timestamp: Date.now()
        };
        this.broadcast(packet);
    }

    broadcast(msg, excludeStream = null) {
        const json = JSON.stringify(msg);
        const data = new TextEncoder().encode(json);

        for (const [client, info] of this.clients.entries()) {
            if (client === excludeStream) continue;
            try {
                client.write(data);
            } catch (e) { console.error(e); }
        }
    }

    sendTo(stream, msg) {
        try {
            const data = new TextEncoder().encode(JSON.stringify(msg));
            stream.write(data);
        } catch (e) {
            console.error("Send error:", e);
        }
    }
}
