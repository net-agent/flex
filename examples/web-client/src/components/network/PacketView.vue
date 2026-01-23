<template>
  <div class="packet-tool">
    <div class="toolbar">
        <h3>Live Packet Monitor</h3>
        <div class="actions">
            <button class="btn-toggle" :class="{ active: isMonitoring }" @click="toggleMonitor">
                <div class="indicator"></div>
                {{ isMonitoring ? 'Monitoring' : 'Paused' }}
            </button>
            <button class="btn-clear" @click="clear">Clear</button>
        </div>
    </div>

    <div class="filter-bar">
        <label class="filter-switch">
             <input type="checkbox" v-model="isFilterEnabled">
             <span>Enable Filters</span>
        </label>

        <template v-if="isFilterEnabled">
            <div class="separator"></div>

            <div class="filter-group">
                <span class="label">CMD:</span>
                <div class="custom-select" ref="cmdDropdownRef">
                    <div class="select-trigger" @click="showCmdDropdown = !showCmdDropdown" :class="{ open: showCmdDropdown }">
                        {{ cmdLabel }} <span class="arrow">▼</span>
                    </div>
                    <div class="select-options" v-if="showCmdDropdown">
                         <label v-for="opt in CMD_OPTIONS" :key="opt.value" class="option-item">
                            <input type="checkbox" :value="opt.value" v-model="filters.cmd">
                            <span>{{ opt.label }}</span>
                        </label>
                    </div>
                </div>
            </div>
            
            <div class="filter-group">
                <span class="label">ACK:</span>
                <select v-model="filters.ack">
                    <option value="all">All</option>
                    <option value="req">Requests</option>
                    <option value="ack">ACKs Only</option>
                </select>
            </div>

            <div class="filter-group">
                <span class="label">SRC:</span>
                <input v-model="filters.src" placeholder="IP:Port or *" class="short-input" />
            </div>
            <div class="filter-group">
                <span class="label">DST:</span>
                <input v-model="filters.dst" placeholder="!Excluded" class="short-input" />
            </div>
            <div class="filter-group">
                <span class="label">LEN:</span>
                <input v-model="filters.len" placeholder=">100" class="mini-input" />
            </div>
        </template>
        <div v-else class="filter-disabled-msg">Filters are off showing all {{ packets.length }} packets</div>
    </div>

    <div class="packet-table-header">
        <div class="col-time">Time</div>
        <div class="col-dir">Dir</div>
        <div class="col-cmd">Command</div>
        <div class="col-src">Source</div>
        <div class="col-dst">Dest</div>
        <div class="col-len">Len</div>
        <div class="col-info">Info</div>
    </div>

    <div class="packet-list" ref="listContainer">
        <div 
            v-for="(pkt, idx) in filteredPackets" 
            :key="idx" 
            class="packet-row" 
            :class="{ rx: pkt.dir === 'rx', tx: pkt.dir === 'tx', selected: isSelected(pkt) }"
            @click="selectPacket(pkt)"
        >
            <div class="col-time">{{ pkt.time }}</div>
            <div class="col-dir">
                <span class="badge" :class="pkt.dir">
                    <ArrowUp v-if="pkt.dir === 'tx'" :size="12" />
                    <ArrowDown v-else :size="12" />
                    {{ pkt.dir === 'tx' ? 'SEND' : 'RECV' }}
                </span>
            </div>
            <div class="col-cmd">{{ getCmdName(pkt.cmd) }}</div>
            <div class="col-src">{{ pkt.srcIP }}:{{ pkt.srcPort }}</div>
            <div class="col-dst">{{ pkt.distIP }}:{{ pkt.distPort }}</div>
            <div class="col-len">{{ pkt.len }}</div>
            <div class="col-info">{{ pkt.info }}</div>
        </div>
    </div>

    <div class="packet-detail" v-if="selectedPacket">
        <div class="detail-header">
            <div class="header-left">
                <h4>Packet Details</h4>
                <div class="view-toggles">
                    <button :class="{ active: viewMode === 'hex' }" @click="viewMode = 'hex'">Hex</button>
                    <button :class="{ active: viewMode === 'text' }" @click="viewMode = 'text'">Text</button>
                    <button :class="{ active: viewMode === 'json' }" @click="viewMode = 'json'" :disabled="!jsonContent">JSON</button>
                </div>
            </div>
            <button class="btn-close" @click="selectedPacket = null">×</button>
        </div>
        <div class="detail-content">
            <div class="hex-view" v-if="viewMode === 'hex'">
                <div v-for="(line, i) in hexDump" :key="i" class="hex-line">
                    <span class="offset">{{ line.offset }}</span>
                    <span class="bytes">{{ line.bytes }}</span>
                    <span class="ascii">{{ line.ascii }}</span>
                </div>
            </div>
            <div class="text-view" v-else-if="viewMode === 'text'">
                <pre>{{ textContent }}</pre>
            </div>
             <div class="json-view" v-else-if="viewMode === 'json'">
                <pre>{{ jsonContent }}</pre>
            </div>
        </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, nextTick } from 'vue';
import { flexService } from '../../services/flex.js';

import { 
    Binary, 
    ArrowUp, ArrowDown 
} from 'lucide-vue-next';

import {
    CMD_OPEN_STREAM, ACK_OPEN_STREAM, 
    CMD_CLOSE_STREAM, ACK_CLOSE_STREAM,
    CMD_PUSH_STREAM_DATA, ACK_PUSH_STREAM_DATA,
    CMD_PING_DOMAIN, ACK_PING_DOMAIN,
    CMD_ACK_FLAG
} from '../../flex/core/packet.js';

const isMonitoring = ref(false);
const isFilterEnabled = ref(false); // Master filter switch
const packets = ref([]);
const selectedPacket = ref(null); 
const listContainer = ref(null);
const MAX_PACKETS = 1000;

// Filters
const filters = reactive({
    cmd: [], // array of selected raw cmd values
    ack: 'all', 
    src: '',
    dst: '',
    len: ''
});

const showCmdDropdown = ref(false);

const CMD_OPTIONS = [
    { value: CMD_PING_DOMAIN, label: 'PING_DOMAIN' },
    { value: CMD_OPEN_STREAM, label: 'OPEN_STREAM' },
    { value: CMD_PUSH_STREAM_DATA, label: 'PUSH_DATA' },
    { value: CMD_CLOSE_STREAM, label: 'CLOSE_STREAM' },
    { value: 0, label: 'HANDSHAKE' },
];

const cmdLabel = computed(() => {
    if (filters.cmd.length === 0) return 'All Commands';
    if (filters.cmd.length === 1) {
        const opt = CMD_OPTIONS.find(o => o.value === filters.cmd[0]);
        return opt ? opt.label : '1 selected';
    }
    return `${filters.cmd.length} selected`;
});

// Mapping
function getCmdName(cmd) {
    const isAck = (cmd & CMD_ACK_FLAG) > 0;
    const raw = cmd & ~CMD_ACK_FLAG;
    let name = "UNKNOWN";
    
    // Find name
    const opt = CMD_OPTIONS.find(o => o.value === raw);
    if (opt) name = opt.label;
    else name = `CMD_${raw}`;
    
    return isAck ? `ACK_${name}` : name;
}

const toggleMonitor = () => {
    isMonitoring.value = !isMonitoring.value;
    if (isMonitoring.value) {
        flexService.node.monitorCallback = handlePacket;
    } else {
        flexService.node.monitorCallback = null;
    }
};

const clear = () => {
    packets.value = [];
    selectedPacket.value = null;
};

const filteredPackets = computed(() => {
    if (!isFilterEnabled.value) return packets.value;

    return packets.value.filter(p => {
        // 1. ACK Filter
        const isAck = (p.cmd & CMD_ACK_FLAG) > 0;
        if (filters.ack === 'req' && isAck) return false;
        if (filters.ack === 'ack' && !isAck) return false;

        // 2. CMD Filter
        if (filters.cmd.length > 0) {
            const raw = p.cmd & ~CMD_ACK_FLAG;
            if (!filters.cmd.includes(raw)) return false;
        }

        // 3. SRC Filter
        const srcKey = `${p.srcIP}:${p.srcPort}`;
        if (!checkFilter(filters.src, srcKey)) return false;
        
        // 4. DST Filter
        const dstKey = `${p.distIP}:${p.distPort}`;
        if (!checkFilter(filters.dst, dstKey)) return false;

        // 5. LEN Filter
        if (filters.len) {
            const val = parseInt(filters.len);
            if (!isNaN(val)) {
                 if (filters.len.startsWith('>')) return p.len > val;
                 if (filters.len.startsWith('<')) return p.len < val;
                 return p.len === val;
            }
        }
        return true;
    });
});

// Advanced Filter Logic
const checkFilter = (pattern, value) => {
    if (!pattern) return true;
    pattern = pattern.trim();
    if (!pattern) return true;

    // Exclusion
    let isExclude = false;
    if (pattern.startsWith('!')) {
        isExclude = true;
        pattern = pattern.substring(1);
    }

    let match = false;
    if (pattern.includes('*')) {
        // Wildcard Regex
        const escaped = pattern.replace(/[.+?^${}()|[\]\\]/g, '\\$&'); 
        // Replace * with .*
        const regexStr = '^' + escaped.replace(/\*/g, '.*') + '$';
        const regex = new RegExp(regexStr, 'i'); // Case insensitive for IPs (hex)
        match = regex.test(value);
    } else {
        // Partial Match
        match = value.toUpperCase().includes(pattern.toUpperCase());
    }

    return isExclude ? !match : match;
};

// Persistence
const saveState = () => {
    const state = {
        isFilterEnabled: isFilterEnabled.value,
        filters: filters,
        // Optional: save isMonitoring? Maybe better to default off or keep last state?
        // User said: "manual start after... like last time". So persis monitoring state conceptually, 
        // but maybe auto-starting can be noisy. Let's persist filters mainly.
        // "Should be consistent with last usage" implied strategy persistence.
    };
    localStorage.setItem('flex_packet_tool_state', JSON.stringify(state));
};

const loadState = () => {
    try {
        const saved = localStorage.getItem('flex_packet_tool_state');
        if (saved) {
            const state = JSON.parse(saved);
            isFilterEnabled.value = state.isFilterEnabled;
            if (state.filters) {
                Object.assign(filters, state.filters);
            }
        }
    } catch (e) {
        console.warn('Failed to load packet tool state', e);
    }
};

// Watch for changes
import { watch } from 'vue';
watch([filters, isFilterEnabled], () => {
    saveState();
}, { deep: true });

const handlePacket = (dir, p) => {
    if (!isMonitoring.value) return;

    // Extract basic info
    const pkt = {
        time: new Date().toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second:'2-digit', fractionalSecondDigits: 3 }),
        dir: dir,
        cmd: p.getCmd(),
        srcIP: p.getSrcIP().toString(16).toUpperCase(), // Hex IP for brevity? Or just number
        srcPort: p.getSrcPort(),
        distIP: p.getDistIP().toString(16).toUpperCase(),
        distPort: p.getDistPort(),
        len: p.getPayloadSize(),
        info: '',
        rawPayload: p.payload ? p.payload.slice(0) : new ArrayBuffer(0) // Copy payload
    };

    // Quick Info Summary
    if ((pkt.cmd & ~CMD_ACK_FLAG) === CMD_PUSH_STREAM_DATA) {
        // Show snippet?
        pkt.info = `Data Chunk (${pkt.len} bytes)`;
    } else if ((pkt.cmd & ~CMD_ACK_FLAG) === CMD_OPEN_STREAM) {
        // Try decode domain
        try {
            const dec = new TextDecoder();
            pkt.info = "Target: " + dec.decode(p.payload);
        } catch(e) {}
    }

    packets.value.push(pkt);
    if (packets.value.length > MAX_PACKETS) {
        const removed = packets.value.shift();
        if (selectedPacket.value === removed) selectedPacket.value = null;
    }

    // Auto scroll
    nextTick(() => {
        if (listContainer.value) {
            listContainer.value.scrollTop = listContainer.value.scrollHeight;
        }
    });
};

const selectPacket = (pkt) => {
    selectedPacket.value = pkt;
};

const isSelected = (pkt) => {
    return selectedPacket.value === pkt;
};

// Remove computed selectedPacket as we now store the ref directly
// const selectedPacket = computed(() => ...);

const viewMode = ref('hex'); // 'hex', 'text', 'json'

const hexDump = computed(() => {
    const pkt = selectedPacket.value;
    if (!pkt) return [];
    
    const lines = [];
    const bytes = new Uint8Array(pkt.rawPayload);
    const ROW_LEN = 16;
    
    for (let i = 0; i < bytes.length; i += ROW_LEN) {
        const chunk = bytes.slice(i, i + ROW_LEN);
        let hex = '';
        let ascii = '';
        
        for (let j = 0; j < ROW_LEN; j++) {
            if (j < chunk.length) {
                const b = chunk[j];
                hex += b.toString(16).padStart(2, '0') + ' ';
                ascii += (b >= 32 && b <= 126) ? String.fromCharCode(b) : '.';
            } else {
                hex += '   ';
            }
        }
        
        lines.push({
            offset: i.toString(16).padStart(4, '0'),
            bytes: hex,
            ascii: ascii
        });
    }
    
    if (lines.length === 0) {
        lines.push({ offset: '0000', bytes: 'Empty Payload', ascii: '' });
    }
    
    return lines;
});

const textContent = computed(() => {
    const pkt = selectedPacket.value;
    if (!pkt || pkt.rawPayload.byteLength === 0) return "Empty Payload";
    try {
        const dec = new TextDecoder('utf-8');
        return dec.decode(pkt.rawPayload);
    } catch (e) {
        return "Error decoding text: " + e.message;
    }
});

const jsonContent = computed(() => {
    const text = textContent.value;
    try {
        const obj = JSON.parse(text);
        return JSON.stringify(obj, null, 2);
    } catch (e) {
        return null;
    }
});

const cmdDropdownRef = ref(null);

const handleClickOutside = (event) => {
    if (showCmdDropdown.value && cmdDropdownRef.value && !cmdDropdownRef.value.contains(event.target)) {
        showCmdDropdown.value = false;
    }
};

onMounted(() => {
    loadState();
    window.addEventListener('click', handleClickOutside);
});

onUnmounted(() => {
    flexService.node.monitorCallback = null;
    window.removeEventListener('click', handleClickOutside);
});
</script>

<style scoped>
.packet-tool {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg-color);
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.85em;
}

.toolbar {
    padding: 12px 16px;
    background: var(--bg-darker);
    border-bottom: 1px solid var(--border-color);
    display: flex;
    justify-content: space-between;
    align-items: center;
}

.toolbar h3 {
    margin: 0;
    font-size: 1em;
}

.actions {
    display: flex;
    gap: 10px;
}

.btn-toggle {
    background: var(--input-bg);
    border: 1px solid var(--border-color);
    color: var(--text-muted);
    padding: 6px 12px;
    border-radius: 4px;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 8px;
    transition: all 0.2s;
}

.indicator {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #666;
}

.btn-toggle.active {
    border-color: var(--accent-color);
    color: var(--text-primary);
}
.btn-toggle.active .indicator {
    background: var(--error-color); /* Red for recording/live */
    box-shadow: 0 0 5px var(--error-color);
}

.btn-clear {
    background: transparent;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    text-decoration: underline;
}

.filter-bar {
    display: flex;
    gap: 16px;
    padding: 10px 16px;
    background: var(--bg-darker);
    border-bottom: 1px solid var(--border-color);
    align-items: center;
    height: 48px;
    box-sizing: border-box;
}

.filter-switch {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    font-size: 0.85em;
    font-weight: 600;
    white-space: nowrap;
}

.separator {
    width: 1px;
    height: 24px;
    background: var(--border-color);
}

.filter-group {
    display: flex;
    align-items: center;
    gap: 8px;
}

.filter-group .label {
    font-size: 0.75em;
    font-weight: 600;
    color: var(--text-muted);
}

.filter-group input, .filter-group select {
    background: var(--input-bg);
    border: 1px solid var(--border-color);
    color: var(--text-primary);
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 0.85em;
}

/* Custom Multi-select */
.custom-select {
    position: relative;
    width: 140px;
}

.select-trigger {
    background: var(--input-bg);
    border: 1px solid var(--border-color);
    color: var(--text-primary);
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 0.85em;
    cursor: pointer;
    display: flex;
    justify-content: space-between;
    align-items: center;
    user-select: none;
}

.select-trigger:hover {
    border-color: var(--text-muted);
}

.select-trigger .arrow {
    font-size: 0.6em;
    color: var(--text-muted);
}

.select-options {
    position: absolute;
    top: 100%;
    left: 0;
    width: 200px;
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.3);
    z-index: 100;
    margin-top: 4px;
    padding: 4px;
    display: flex;
    flex-direction: column;
    gap: 2px;
}

.option-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    cursor: pointer;
    border-radius: 4px;
}

.option-item:hover {
    background: var(--bg-hover);
}

.option-item input {
    margin: 0;
    cursor: pointer;
}

.short-input { width: 80px; }
.mini-input { width: 50px; }

.filter-disabled-msg {
    color: var(--text-muted);
    font-size: 0.85em;
    font-style: italic;
}

/* Table */
.packet-table-header {
    display: flex;
    background: var(--panel-bg);
    padding: 8px 16px;
    font-weight: 600;
    color: var(--text-secondary);
    border-bottom: 1px solid var(--border-color);
}

.col-time { width: 100px; flex-shrink: 0; }
.col-dir { width: 80px; flex-shrink: 0; }
.col-cmd { width: 140px; flex-shrink: 0; }
.col-src { width: 100px; flex-shrink: 0; }
.col-dst { width: 100px; flex-shrink: 0; }
.col-len { width: 60px; flex-shrink: 0; text-align: right; }
.col-info { flex: 1; margin-left: 20px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--text-muted); }

.packet-list {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
    background: var(--bg-color);
}

.packet-row {
    display: flex;
    padding: 4px 16px;
    border-bottom: 1px solid rgba(255,255,255,0.03);
    cursor: pointer;
    align-items: center;
}

.packet-row:hover {
    background: var(--bg-hover);
}

.packet-row.selected {
    background: var(--bg-hover); /* Use theme variable */
    border-left: 3px solid var(--accent-color);
}
/* Ensure text is readable in light mode */
.packet-row {
    color: var(--text-primary);
}

.badge {
    font-size: 0.75em;
    padding: 2px 6px;
    border-radius: 4px;
    font-weight: 700;
    display: flex;
    align-items: center;
    gap: 4px;
    width: fit-content;
}
.badge.rx { 
    background: rgba(239, 68, 68, 0.1); 
    color: var(--error-color); /* Red */
} 
.badge.tx { 
    background: rgba(59, 130, 246, 0.1); 
    color: #3b82f6; /* Blue */
}

/* Detail View */
.packet-detail {
    height: 250px;
    border-top: 1px solid var(--border-color);
    background: var(--panel-bg);
    display: flex;
    flex-direction: column;
}

.detail-header {
    padding: 8px 16px;
    background: var(--bg-darker);
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-bottom: 1px solid var(--border-color);
}

.header-left {
    display: flex;
    align-items: center;
    gap: 16px;
}

.detail-header h4 { margin: 0; font-size: 0.9em; }
.btn-close { background: none; border: none; font-size: 1.2em; color: var(--text-muted); cursor: pointer; }

.view-toggles {
    display: flex;
    background: var(--bg-color);
    border-radius: 4px;
    padding: 2px;
    border: 1px solid var(--border-color);
}

.view-toggles button {
    background: transparent;
    border: none;
    color: var(--text-muted);
    padding: 4px 10px;
    font-size: 0.8em;
    cursor: pointer;
    border-radius: 2px;
}
.view-toggles button.active {
    background: var(--accent-light);
    color: var(--accent-color);
    font-weight: 600;
}
.view-toggles button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
}

.detail-content {
    flex: 1;
    overflow-y: auto;
    padding: 10px;
}

.hex-view, .text-view, .json-view {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.85em;
    color: var(--text-primary);
}

.text-view pre, .json-view pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-all;
}

.hex-view {
    display: table;
    width: 100%;
}
.hex-line {
    display: table-row;
}
.hex-line span {
    display: table-cell;
    padding-right: 20px;
}
.offset { color: var(--accent-color); user-select: none; width: 60px; }
.bytes { color: var(--text-primary); }
.ascii { color: var(--text-secondary); }

</style>
