const DB_NAME = 'flex_chat_db';
const DB_VERSION = 1;

class ChatStorageService {
    constructor() {
        this.db = null;
        this.readyPromise = this._init();
    }

    _init() {
        return new Promise((resolve, reject) => {
            const request = indexedDB.open(DB_NAME, DB_VERSION);

            request.onerror = (event) => {
                console.error('[ChatStorage] DB Error:', event.target.error);
                reject(event.target.error);
            };

            request.onsuccess = (event) => {
                this.db = event.target.result;
                console.log('[ChatStorage] DB Ready');
                resolve();
            };

            request.onupgradeneeded = (event) => {
                const db = event.target.result;

                // Rooms Store: id, name, isHost, lastSeqId, createdAt
                if (!db.objectStoreNames.contains('rooms')) {
                    const roomsStore = db.createObjectStore('rooms', { keyPath: 'id' });
                    roomsStore.createIndex('updatedAt', 'updatedAt', { unique: false });
                }

                // Messages Store: id, roomId, seqId, timestamp
                if (!db.objectStoreNames.contains('messages')) {
                    const msgStore = db.createObjectStore('messages', { keyPath: 'id' });
                    msgStore.createIndex('roomId', 'roomId', { unique: false });
                    msgStore.createIndex('roomId_seqId', ['roomId', 'seqId'], { unique: true });
                }
            };
        });
    }

    async ready() {
        return this.readyPromise;
    }

    // --- Room Operations ---

    async saveRoom(room) {
        await this.ready();
        return new Promise((resolve, reject) => {
            const tx = this.db.transaction(['rooms'], 'readwrite');
            const store = tx.objectStore('rooms');
            const data = {
                ...room,
                updatedAt: Date.now()
            };
            const req = store.put(data);
            req.onsuccess = () => resolve(data);
            req.onerror = () => reject(req.error);
        });
    }

    async deleteRoom(id) {
        await this.ready();
        return new Promise((resolve, reject) => {
            const tx = this.db.transaction(['rooms'], 'readwrite');
            const store = tx.objectStore('rooms');
            const req = store.delete(id);
            req.onsuccess = () => resolve();
            req.onerror = () => reject(req.error);
        });
    }

    async getRoom(id) {
        await this.ready();
        return new Promise((resolve, reject) => {
            const tx = this.db.transaction(['rooms'], 'readonly');
            const store = tx.objectStore('rooms');
            const req = store.get(id);
            req.onsuccess = () => resolve(req.result);
            req.onerror = () => reject(req.error);
        });
    }

    async getAllRooms() {
        await this.ready();
        return new Promise((resolve, reject) => {
            const tx = this.db.transaction(['rooms'], 'readonly');
            const store = tx.objectStore('rooms');
            const req = store.getAll();
            req.onsuccess = () => resolve(req.result);
            req.onerror = () => reject(req.error);
        });
    }

    // --- Message Operations ---

    async saveMessage(roomId, message) {
        await this.ready();
        return new Promise((resolve, reject) => {
            const tx = this.db.transaction(['messages'], 'readwrite');
            const store = tx.objectStore('messages');

            // Ensure roomId is attached
            const data = { ...message, roomId };

            const req = store.put(data);
            req.onsuccess = () => resolve(data);
            req.onerror = () => reject(req.error);
        });
    }

    /**
     * Get messages for a room, optionally starting from a specific seqId (exclusive)
     */
    async getMessages(roomId, sinceSeqId = -1) {
        await this.ready();
        return new Promise((resolve, reject) => {
            const tx = this.db.transaction(['messages'], 'readonly');
            const store = tx.objectStore('messages');
            const index = store.index('roomId_seqId');

            // Range: roomId matched, seqId > sinceSeqId
            // IDBKeyRange.bound(lower, upper, lowerOpen, upperOpen)
            // We want [roomId, sinceSeqId] -> [roomId, Infinity]
            // But strict seqId filtering is easier done in memory or strict range if index supports array keys well.
            // A simpler way with compound index ['roomId', 'seqId']:
            // Lower bound: [roomId, sinceSeqId + 0.1] (effectively > sinceSeqId)
            // Upper bound: [roomId, Infinity]

            // Since seqId is integer, we can use sinceSeqId + 1 (inclusive) 
            // OR just use open range (sinceSeqId, infinity).
            // Let's rely on standard IDB bounds.

            // Correct approach for compound index queries on a prefix + number:
            // bound([roomId, sinceSeqId], [roomId, Infinity], true, false)
            // true/false for open/closed. 3rd arg is lowerOpen.
            // so bound([roomId, sinceSeqId], [roomId, Infinity], true, false) means:
            // > [roomId, sinceSeqId] AND <= [roomId, Infinity]

            const range = IDBKeyRange.bound(
                [roomId, sinceSeqId],
                [roomId, Number.MAX_SAFE_INTEGER],
                true, // lowerOpen: true => > sinceSeqId
                false // upperOpen: false (inclusive, though infinity doesn't matter)
            );

            const req = index.getAll(range);
            req.onsuccess = () => resolve(req.result);
            req.onerror = () => reject(req.error);
        });
    }

    async getLastMessageSeqId(roomId) {
        await this.ready();
        return new Promise((resolve, reject) => {
            const tx = this.db.transaction(['messages'], 'readonly');
            const store = tx.objectStore('messages');
            const index = store.index('roomId_seqId');

            // Open cursor direction 'prev' to get last item
            const range = IDBKeyRange.bound(
                [roomId, -1],
                [roomId, Number.MAX_SAFE_INTEGER]
            );

            const req = index.openCursor(range, 'prev');
            req.onsuccess = (e) => {
                const cursor = e.target.result;
                if (cursor) {
                    resolve(cursor.value.seqId);
                } else {
                    resolve(0); // No messages
                }
            };
            req.onerror = () => reject(req.error);
        });
    }
}

export const chatStorage = new ChatStorageService();
