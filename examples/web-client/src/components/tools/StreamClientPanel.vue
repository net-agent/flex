<template>
    <div class="panel-column client-column">
        <div class="column-header">
            <ArrowUpRight :size="18" />
            <span>Client (Dialer)</span>
        </div>

        <!-- Client Controls -->
        <div class="control-box">
            <div class="input-row">
                <div class="input-wrapper">
                    <label>Target</label>
                    <input v-model="targetDomain" type="text" placeholder="Domain" class="flex-input" />
                </div>
                <div class="input-wrapper sm">
                    <label>Port</label>
                    <input v-model.number="targetPort" type="number" placeholder="80" class="flex-input" />
                </div>
            </div>
            <button @click="toggleConnection" :class="['flex-btn', isConnected ? 'danger' : 'primary']">
                {{ isConnected ? 'Disconnect' : 'Connect' }}
                <Loader2 v-if="isConnecting" class="spin ml-2" :size="16" />
            </button>
        </div>

        <!-- Client Message Send -->
        <div class="control-box" v-if="isConnected">
            <div class="send-row">
                <input 
                    v-model="clientMessage" 
                    @keyup.enter="sendClientMessage"
                    type="text" 
                    placeholder="Type message..." 
                    class="flex-input full" 
                />
                <button @click="sendClientMessage" class="flex-btn secondary">
                    <Send :size="16" />
                </button>
            </div>
        </div>

        <!-- Client Logs -->
        <div class="logs-container">
            <div class="logs-header">
                <span>Activity Log</span>
                <button @click="clientLogs = []" class="icon-btn-sm" title="Clear">
                    <Trash2 :size="14" />
                </button>
            </div>
            <div class="logs-list" ref="clientLogsRef">
                <div v-for="(log, idx) in clientLogs" :key="idx" :class="['log-entry', log.type]">
                    <span class="timestamp">{{ log.time }}</span>
                    <div class="log-content">
                        <component :is="getLogIcon(log.type)" :size="14" class="log-icon" />
                        <span>{{ log.message }}</span>
                    </div>
                </div>
                 <div v-if="clientLogs.length === 0" class="empty-state">
                    <span>No activity</span>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, onUnmounted, nextTick } from 'vue';
import { flexService } from '../../services/flex.js';
import { Loader2, Send, Trash2, ArrowRight, ArrowLeft, Plug, XCircle, Info, ArrowUpRight } from 'lucide-vue-next';

// --- Client State ---
const targetDomain = ref('localhost');
const targetPort = ref(8080);
const isConnecting = ref(false);
const isConnected = ref(false);
const clientStream = ref(null);
const clientMessage = ref('');
const clientLogs = ref([]);
const clientLogsRef = ref(null);

// --- Helpers ---
const addLog = (type, message) => {
    clientLogs.value.push({
        type,
        message,
        time: new Date().toLocaleTimeString()
    });
    nextTick(() => {
        if (clientLogsRef.value) {
            clientLogsRef.value.scrollTop = clientLogsRef.value.scrollHeight;
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

// --- Client Logic ---
const toggleConnection = async () => {
    if (isConnected.value) {
        if (clientStream.value) {
            clientStream.value.close();
            clientStream.value = null; // We set it null here, 'close' event handles the rest
        }
    } else {
        await connectStream();
    }
};

const connectStream = async () => {
    if (!flexService.node.ip) {
        addLog('error', 'Node not connected');
        return;
    }
    
    isConnecting.value = true;
    try {
        addLog('info', `Dialing ${targetDomain.value}:${targetPort.value}...`);
        const s = await flexService.node.dial(targetDomain.value, targetPort.value);
        
        clientStream.value = s;
        isConnected.value = true;
        addLog('connect', 'Connected!');

        // Bind Events
        s.on('data', (data) => {
            const txt = new TextDecoder().decode(data);
            addLog('rx', `RX: ${txt}`);
        });
        s.on('close', () => {
             addLog('disconnect', 'Disconnected');
             isConnected.value = false;
             clientStream.value = null;
        });
        s.on('error', (err) => {
             addLog('error', `Error: ${err}`);
        });

    } catch (e) {
        addLog('error', `Dial Failed: ${e.message}`);
    } finally {
        isConnecting.value = false;
    }
};

const sendClientMessage = () => {
    if (!clientStream.value || !clientMessage.value) return;
    try {
        const msg = clientMessage.value;
        clientStream.value.write(msg);
        addLog('tx', `TX: ${msg}`);
        clientMessage.value = '';
    } catch (e) {
        addLog('error', `Send Failed: ${e.message}`);
    }
};

// --- Cleanup ---
onUnmounted(() => {
    if (clientStream.value) clientStream.value.close();
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
    color: var(--accent-color);
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
