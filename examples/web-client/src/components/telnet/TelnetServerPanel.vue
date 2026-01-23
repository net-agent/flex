<template>
    <div class="panel-column server-column">
        <div class="column-header">
            <ArrowDownLeft :size="18" />
            <span>Server</span>
        </div>

        <!-- Server Controls -->
        <div class="control-box">
            <div class="input-row">
                <div class="input-wrapper">
                    <label>Listen Port</label>
                    <input v-model.number="listenPort" type="number" placeholder="8080" class="flex-input" :disabled="isListening" />

                </div>
                 <div class="input-wrapper">
                    <label>Secret (Optional)</label>
                    <input v-model="listenSecret" type="password" placeholder="Encryption Key" class="flex-input" :disabled="isListening" />
                </div>
                <button @click="toggleListen" :class="['flex-btn', isListening ? 'danger' : 'primary']">

                    {{ isListening ? 'Stop Listening' : 'Start Listening' }}
                </button>
            </div>
        </div>

        <!-- Server Session & Send -->
        <div class="control-box" v-if="isListening">
            <div class="input-group">
                 <div class="input-wrapper">
                    <label>Active Session</label>
                    <select v-model="selectedSessionId" class="flex-input">
                        <option value="" disabled>{{ serverSessions.length === 0 ? 'No connections' : 'Select a session' }}</option>
                        <option v-for="s in serverSessions" :key="s.id" :value="s.id">
                            {{ s.label }}
                        </option>
                    </select>
                </div>
            </div>
            <div class="send-row" v-if="selectedSessionId">
                <input 
                    v-model="serverMessage" 
                    @keyup.enter="sendServerMessage"
                    type="text" 
                    placeholder="Type message..." 
                    class="flex-input full" 
                />
                <button @click="sendServerMessage" class="flex-btn secondary">
                    <Send :size="16" />
                </button>
            </div>
        </div>

         <!-- Server Logs -->
         <div class="logs-container">
            <div class="logs-header">
                <span>Activity Log</span>
                <button @click="serverLogs = []" class="icon-btn-sm" title="Clear">
                    <Trash2 :size="14" />
                </button>
            </div>
            <div class="logs-list" ref="serverLogsRef">
                <div v-for="(log, idx) in serverLogs" :key="idx" :class="['log-entry', log.type]">
                    <span class="timestamp">{{ log.time }}</span>
                    <div class="log-content">
                        <component :is="getLogIcon(log.type)" :size="14" class="log-icon" />
                        <span>{{ log.message }}</span>
                    </div>
                </div>
                 <div v-if="serverLogs.length === 0" class="empty-state">
                    <span>Waiting for connections...</span>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, onUnmounted, nextTick } from 'vue';
import { flexService } from '../../services/flex.js';
import { Send, Trash2, ArrowRight, ArrowLeft, Plug, XCircle, Info, ArrowDownLeft } from 'lucide-vue-next';

import { FLEX_STREAM_PORT } from '../../flex/core/constants.js';


// --- Server State ---
const listenPort = ref(FLEX_STREAM_PORT);
const listenSecret = ref('');
const isListening = ref(false);

const serverSessions = ref([]); // Array of { id, stream, label }
const selectedSessionId = ref('');
const serverMessage = ref('');
const serverLogs = ref([]);
const serverLogsRef = ref(null);

// --- Helpers ---
const addLog = (type, message) => {
    serverLogs.value.push({
        type,
        message,
        time: new Date().toLocaleTimeString()
    });
    nextTick(() => {
        if (serverLogsRef.value) {
            serverLogsRef.value.scrollTop = serverLogsRef.value.scrollHeight;
        }
    });
};

const getLogIcon = (type) => {
    switch (type) {
        case 'tx': return ArrowRight;
        case 'rx': return ArrowLeft;
        case 'connect': return Plug;
        case 'disconnect': return XCircle;
        default: return Info;
    }
};

// --- Server Logic ---
const toggleListen = () => {
    if (isListening.value) {
        flexService.node.listenhub.close(listenPort.value);
        isListening.value = false;
        addLog('info', `Stopped listening on ${listenPort.value}`);
        // Clear sessions
        serverSessions.value.forEach(s => s.stream.close());
        serverSessions.value = [];
        selectedSessionId.value = '';
    } else {
        startListening();
    }
};

const startListening = () => {
    if (!flexService.node.ip) {
       addLog('error', 'Node not connected');
       return;
    }

    const port = listenPort.value;
    const secret = listenSecret.value;
    
    const onStream = (stream) => {
        const label = `${stream.remoteDomain}:${stream.remotePort}`;
        const sid = Math.random().toString(36).substr(2, 9);
        
        addLog('connect', `Accept: ${label} ${secret ? '(Secure)' : ''}`);
        
        const session = { id: sid, stream, label };
        serverSessions.value.push(session);
        
        // Auto-select if first
        if (serverSessions.value.length === 1) {
            selectedSessionId.value = sid;
        }

        // Bind Events
        stream.on('data', (data) => {
            const txt = new TextDecoder().decode(data);
            addLog('rx', `[${label}] RX: ${txt}`);
        });

        stream.on('close', () => {
             addLog('disconnect', `[${label}] Disconnected`);
             const idx = serverSessions.value.findIndex(s => s.id === sid);
             if (idx !== -1) {
                 serverSessions.value.splice(idx, 1);
             }
             if (selectedSessionId.value === sid) {
                 selectedSessionId.value = serverSessions.value.length > 0 ? serverSessions.value[0].id : '';
             }
        });
        
        stream.on('error', (e) => addLog('error', `[${label}] Err: ${e}`));
    };

    const success = flexService.node.listen(port, secret, onStream);



    if (success) {
        isListening.value = true;
        addLog('info', `Listening on port ${port}...`);
    } else {
        addLog('error', `Failed to listen on ${port}`);
    }
};

const sendServerMessage = () => {
    const session = serverSessions.value.find(s => s.id === selectedSessionId.value);
    if (!session || !serverMessage.value) return;

    try {
        const msg = serverMessage.value;
        session.stream.write(msg);
        addLog('tx', `[${session.label}] TX: ${msg}`);
        serverMessage.value = '';
    } catch (e) {
         addLog('error', `Send Failed: ${e.message}`);
    }
};

// --- Cleanup ---
onUnmounted(() => {
    if (isListening.value) flexService.node.listenhub.close(listenPort.value);
});
</script>

<style scoped>
.panel-column {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-lg);
    overflow: hidden;
    box-shadow: var(--shadow-sm);
}

.column-header {
    padding: 12px 16px;
    background: var(--bg-color);
    border-bottom: 1px solid var(--border-color);
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--success-color);
}

.control-box {
    padding: 16px;
    border-bottom: 1px solid var(--border-color);
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.input-row {
    display: flex;
    gap: 10px;
    align-items: flex-end;
}

.input-wrapper {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 6px;
}
.input-wrapper.sm { flex: 0 0 80px; }

.input-wrapper label {
    font-size: 0.8em;
    font-weight: 500;
    color: var(--text-secondary);
}

.flex-input {
    width: 100%;
    background: var(--input-bg);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-md);
    color: var(--text-primary);
    padding: 8px 12px;
    font-family: inherit;
    outline: none;
    transition: border-color 0.2s;
    font-size: 0.95em;
}
.flex-input:focus { border-color: var(--accent-color); }

.flex-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 16px;
    border-radius: var(--radius-md);
    font-weight: 500;
    cursor: pointer;
    border: none;
    height: 38px;
    transition: all 0.2s;
    white-space: nowrap;
    font-size: 0.9em;
}

.flex-btn.primary { background: var(--accent-color); color: white; }
.flex-btn.primary:hover { background: var(--accent-hover); }
.flex-btn.danger { background: rgba(239, 68, 68, 0.1); color: #ef4444; }
.flex-btn.danger:hover { background: rgba(239, 68, 68, 0.2); }
.flex-btn.secondary { background: var(--bg-color); border: 1px solid var(--border-color); color: var(--text-primary); }
.flex-btn.secondary:hover { border-color: var(--accent-color); color: var(--accent-color); }

.send-row {
    display: flex;
    gap: 8px;
}

/* Logs */
.logs-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background: var(--bg-color);
}

.logs-header {
    padding: 8px 16px;
    border-bottom: 1px solid var(--border-color);
    font-size: 0.8em;
    font-weight: 600;
    color: var(--text-secondary);
    display: flex;
    justify-content: space-between;
    align-items: center;
    background: var(--panel-bg);
}

.logs-list {
    flex: 1;
    overflow-y: auto;
    padding: 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.85em;
}

.log-entry {
    display: flex;
    gap: 12px;
    padding: 4px 0;
    border-bottom: 1px dashed var(--border-color);
}
.log-entry:last-child { border-bottom: none; }

.timestamp { color: var(--text-muted); min-width: 70px; font-size: 0.9em; }
.log-content { flex: 1; display: flex; align-items: flex-start; gap: 6px; word-break: break-all; }
.log-icon { flex-shrink: 0; margin-top: 2px; }

/* Log Colors */
.log-entry.tx { color: var(--accent-color); }
.log-entry.rx { color: var(--success-color); }
.log-entry.error { color: #ef4444; }
.log-entry.connect { color: var(--success-color); }
.log-entry.disconnect { color: #ef4444; }

.empty-state {
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
    font-style: italic;
    opacity: 0.7;
}

.icon-btn-sm {
     background: transparent;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 4px;
    border-radius: var(--radius-sm);
}
.icon-btn-sm:hover { color: var(--text-primary); background: var(--bg-color); }

.spin { animation: spin 1s linear infinite; }
@keyframes spin { 100% { transform: rotate(360deg); } }
.ml-2 { margin-left: 8px; }
</style>
