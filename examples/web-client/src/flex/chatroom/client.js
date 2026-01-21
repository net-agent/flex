export class ChatClient {
    constructor(node) {
        this.node = node;
        this.stream = null;
        this.onMessage = (msg) => { }; // Callback
        this.onUserList = (users) => { }; // Callback
        this.onRoomInfo = (info) => { }; // Callback
        this.onStateChange = (state) => { }; // connected, disconnected
    }

    async connect(domain, port, nickname) {
        try {
            this.stream = await this.node.dial(domain, port);
            console.log("[ChatClient] Dial success");

            this.stream.on('data', (chunk) => {
                const text = new TextDecoder().decode(chunk);
                try {
                    console.log("[ChatClient] RX:", text);
                    const msg = JSON.parse(text);

                    if (msg.type === 'user_list') {
                        if (this.onUserList) this.onUserList(msg.users);
                        return;
                    }

                    if (msg.type === 'room_info') {
                        if (this.onRoomInfo) this.onRoomInfo(msg);
                        return;
                    }

                    if (msg.type === 'sync_resp') {
                        console.log(`[ChatClient] Sync Resp (${msg.messages.length} msgs)`);
                        if (this.onSyncResp) this.onSyncResp(msg);
                        return;
                    }

                    if (this.onMessage) this.onMessage(msg);
                } catch (e) {
                    console.error("[ChatClient] Parse Error:", e);
                }
            });

            this.stream.on('close', () => {
                console.log("[ChatClient] Disconnected");
                this.stream = null;
                if (this.onStateChange) this.onStateChange('disconnected');
            });

            // Send Join Packet
            if (nickname) {
                this.sendPacket({
                    type: 'join',
                    nickname: nickname
                });
            }

            if (this.onStateChange) this.onStateChange('connected');
            return true;
        } catch (e) {
            console.error("[ChatClient] Connection Failed:", e);
            return false;
        }
    }

    sendMessage(content) {
        return this.sendPacket({
            type: 'msg',
            content: content
        });
    }

    sendSyncRequest(lastSeqId) {
        return this.sendPacket({
            type: 'sync_req',
            lastSeqId: lastSeqId
        });
    }

    sendPacket(obj) {
        if (!this.stream) return false;
        const data = new TextEncoder().encode(JSON.stringify(obj));
        try {
            this.stream.write(data);
            return true;
        } catch (e) {
            console.error("[ChatClient] Send Failed:", e);
            return false;
        }
    }

    disconnect() {
        if (this.stream) {
            this.stream.close();
            this.stream = null;
            if (this.onStateChange) this.onStateChange('disconnected');
        }
    }

    get isConnected() {
        return !!this.stream;
    }
}
