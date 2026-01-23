<template>
  <ToolLayout>
    <template #icon>
        <Activity :size="20" />
    </template>
    <template #title>Ping Tool</template>
    
    <div class="ping-container">
        <div class="content-wrapper">
            <!-- Tabs -->
            <div class="tabs">
                <button 
                    :class="['tab-btn', currentTab === 'single' ? 'active' : '']" 
                    @click="currentTab = 'single'"
                >
                    Single Target
                </button>
                <button 
                    :class="['tab-btn', currentTab === 'batch' ? 'active' : '']" 
                    @click="currentTab = 'batch'"
                >
                    Batch Ping
                </button>
                <button 
                    :class="['tab-btn', currentTab === 'monitor' ? 'active' : '']" 
                    @click="currentTab = 'monitor'"
                >
                    Monitor
                </button>
            </div>

            <!-- Monitor Mode (v-show to keep running in background) -->
            <AliveMonitorPanel v-show="currentTab === 'monitor'" />

            <!-- Single Mode -->
            <div v-if="currentTab === 'single'" class="mode-panel">
                <div class="input-group">
                    <div class="input-wrapper">
                        <Globe class="input-icon" :size="16" />
                        <input v-model="target" @keyup.enter="startSinglePing" placeholder="Target Domain (e.g. local)" :disabled="isPinging || isContinuousRunning" />
                    </div>
                    
                    <div class="control-group">
                        <label class="checkbox-label" title="Continuous Ping">
                            <input type="checkbox" v-model="isContinuous" :disabled="isContinuousRunning" />
                            <span>Loop</span>
                        </label>
                        <input 
                            v-if="isContinuous" 
                            v-model.number="interval" 
                            type="number" 
                            min="100" 
                            step="100" 
                            class="interval-input" 
                            title="Interval (ms)" 
                            :disabled="isContinuousRunning"
                        />
                        <span v-if="isContinuous" class="unit">ms</span>
                    </div>

                    <button class="btn-primary" @click="toggleSinglePing" :disabled="!target">
                        <span v-if="!isContinuousRunning && !isPinging">Ping</span>
                        <span v-else-if="isContinuousRunning">Stop</span>
                        <Loader2 v-else class="spin" :size="16" />
                    </button>
                </div>
            </div>

            <!-- Batch Mode -->
            <div v-if="currentTab === 'batch'" class="mode-panel">
                 <div class="batch-input">
                    <textarea 
                        v-model="batchTargets" 
                        placeholder="Enter domains, one per line..." 
                        rows="5"
                        :disabled="isBatchRunning"
                    ></textarea>
                 </div>
                 <div class="batch-actions">
                    <button class="btn-primary" @click="runBatchPing" :disabled="!batchTargets || isBatchRunning">
                        <span v-if="!isBatchRunning">Ping All</span>
                        <Loader2 v-else class="spin" :size="16" />
                    </button>
                 </div>
            </div>
            
            <!-- History -->
            <!-- History Section -->
            <div class="ping-history">
                <!-- Single Mode History -->
                <template v-if="currentTab === 'single'">
                    <div v-for="(res, idx) in singleHistory" :key="idx" :class="['ping-item', res.error ? 'error' : 'success']">
                        <span class="timestamp">{{ res.time }}</span>
                        <span class="domain">{{ res.domain }}</span>
                        <div class="result-badge" v-if="!res.error">
                            <CheckCircle2 :size="14" />
                            <span>{{ res.latency }}ms</span>
                        </div>
                        <div class="result-badge error" v-else>
                            <AlertCircle :size="14" />
                            <span>{{ res.error }}</span>
                        </div>
                    </div>
                     <div v-if="singleHistory.length === 0" class="empty-hint">
                        <MousePointerClick :size="48" />
                        <p>Enter a domain to start</p>
                    </div>
                </template>

                 <!-- Batch Mode History -->
                <template v-if="currentTab === 'batch'">
                    <div v-for="run in batchHistory" :key="run.id" class="batch-run">
                        <div class="batch-header" @click="toggleBatchExpand(run)">
                            <div class="batch-meta">
                                <component :is="run.expanded ? ChevronDown : ChevronRight" :size="18" class="expand-icon" />
                                <span class="time">{{ run.time }}</span>
                                <Layers :size="14" class="icon-sm" />
                                <span class="summary">Batch Run ({{ run.finished }}/{{ run.total }})</span>
                            </div>
                            <!-- Progress Bar if running -->
                            <div class="progress-wrapper" v-if="run.finished < run.total">
                                <Loader2 class="spin" :size="14" />
                            </div>
                        </div>

                        <div v-if="run.expanded && run.results.length > 0" class="batch-results">
                            <div v-for="(res, idx) in run.results" :key="idx" :class="['ping-item', 'batch-item', res.error ? 'error' : 'success']">
                                <span class="domain">{{ res.domain }}</span>
                                <div class="result-badge" v-if="!res.error">
                                    <CheckCircle2 :size="14" />
                                    <span>{{ res.latency }}ms</span>
                                </div>
                                <div class="result-badge error" v-else>
                                    <AlertCircle :size="14" />
                                    <span>{{ res.error }}</span>
                                </div>
                            </div>
                        </div>
                         <div v-if="run.expanded && run.results.length === 0" class="batch-empty">
                            <span>Starting...</span>
                        </div>
                    </div>
                     <div v-if="batchHistory.length === 0" class="empty-hint">
                        <Layers :size="48" />
                        <p>Run a batch to see aggregated logs</p>
                    </div>
                </template>
            </div>
        </div>
    </div>
  </ToolLayout>
</template>

<script>
export default {
    name: 'PingView'
}
</script>

<script setup>
import { ref, onUnmounted, computed } from 'vue';
import { flexService } from '../../services/flex.js';
import { Activity, Globe, Loader2, CheckCircle2, AlertCircle, MousePointerClick, ChevronRight, ChevronDown, Layers } from 'lucide-vue-next';
import ToolLayout from '../common/ToolLayout.vue';
import AliveMonitorPanel from './AliveMonitorPanel.vue';

// State
const currentTab = ref('single'); // 'single' | 'batch'

// Separate Histories
const singleHistory = ref([]);
const batchHistory = ref([]);

// Single Mode State
const target = ref('');
const isPinging = ref(false); // For single, one-off ping
const isContinuous = ref(false);
const isContinuousRunning = ref(false);
const interval = ref(1000);
let timerId = null;

// Batch Mode State
const batchTargets = ref('');
const isBatchRunning = ref(false);


// --- Single Mode Logic ---

const toggleSinglePing = () => {
    if (isContinuous.value) {
        if (isContinuousRunning.value) {
            stopContinuous();
        } else {
            startContinuous();
        }
    } else {
        doPingOnce();
    }
};

const startSinglePing = () => {
    if (!isContinuous.value) doPingOnce();
};

const doPingOnce = async () => {
    if (!target.value) return;
    isPinging.value = true;
    await executePing(target.value, false);
    isPinging.value = false;
};

const startContinuous = () => {
    if (!target.value) return;
    isContinuousRunning.value = true;
    executePing(target.value, false); // Immediate first
    timerId = setInterval(() => {
        executePing(target.value, false);
    }, interval.value);
};

const stopContinuous = () => {
    isContinuousRunning.value = false;
    if (timerId) {
        clearInterval(timerId);
        timerId = null;
    }
};

// --- Batch Mode Logic ---

const runBatchPing = async () => {
    if (!batchTargets.value) return;
    
    const domains = batchTargets.value
        .split('\n')
        .map(d => d.trim())
        .filter(d => d.length > 0);
        
    if (domains.length === 0) return;
    
    // Create new batch run entry
    const batchRun = {
        id: Date.now(),
        time: new Date().toLocaleTimeString(),
        total: domains.length,
        finished: 0,
        results: [],
        expanded: true // Auto-expand new run
    };
    batchHistory.value.unshift(batchRun);
    if (batchHistory.value.length > 50) batchHistory.value.pop();
    
    isBatchRunning.value = true;
    try {
        // Run parallel but track results into the specific batch run
        await Promise.all(domains.map(d => executeBatchPing(d, batchRun)));
    } finally {
        isBatchRunning.value = false;
    }
};

const toggleBatchExpand = (run) => {
    run.expanded = !run.expanded;
};

// --- Core Helper ---

const executePing = async (domain, isBatch) => {
    // This function is now only for Single ping. 
    // Batch logic uses executeBatchPing to capture closure of 'batchRun'.
    const now = new Date().toLocaleTimeString();
    try {
        const ms = await flexService.ping(domain);
        addToHistory({
            time: now,
            domain: domain,
            latency: ms,
            error: null
        });
    } catch (err) {
        addToHistory({
            time: now,
            domain: domain,
            latency: null,
            error: err.message
        });
    }
};

const executeBatchPing = async (domain, batchRun) => {
    const now = new Date().toLocaleTimeString();
    let result = {
        time: now,
        domain: domain,
        latency: null,
        error: null
    };

    try {
        const ms = await flexService.ping(domain);
        result.latency = ms;
    } catch (err) {
        result.error = err.message;
    }
    
    // Update Batch Run State
    batchRun.results.push(result);
    batchRun.finished++;
};

const addToHistory = (item) => {
    singleHistory.value.unshift(item);
    if (singleHistory.value.length > 100) singleHistory.value.pop();
};

onUnmounted(() => {
    stopContinuous();
});

</script>

<style scoped>
.ping-container {
    height: 100%;
    padding: 24px;
    overflow-y: auto;
}

.content-wrapper {
    max-width: 800px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    height: 100%;
    gap: 20px;
}

/* Tabs */
.tabs {
    display: flex;
    gap: 10px;
    border-bottom: 1px solid var(--border-color);
    padding-bottom: 10px;
}

.tab-btn {
    background: transparent;
    border: none;
    padding: 8px 16px;
    font-size: 0.95em;
    font-weight: 500;
    color: var(--text-secondary);
    border-radius: var(--radius-md);
    cursor: pointer;
}

.tab-btn:hover {
    background: var(--bg-color);
    color: var(--text-primary);
}

.tab-btn.active {
    background: var(--accent-light);
    color: var(--accent-color);
    font-weight: 600;
}

/* Mode Panels */
.mode-panel {
    background: var(--panel-bg);
    padding: 20px;
    border: 1px solid var(--border-color);
    border-radius: var(--radius-md);
}

/* Single Mode Input */
.input-group {
    display: flex;
    gap: 12px;
    align-items: center;
}

.input-wrapper {
    flex: 1;
    position: relative;
    display: flex;
    align-items: center;
}

.input-icon {
    position: absolute;
    left: 12px;
    color: var(--text-muted);
}

.input-group input {
    width: 100%;
    padding: 10px 10px 10px 38px;
    background: var(--input-bg);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-md);
    font-size: 1em;
}

.control-group {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 10px;
    background: var(--bg-color);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-md);
    height: 42px;
}

.checkbox-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.9em;
    cursor: pointer;
    user-select: none;
    margin-right: 5px;
}

.interval-input {
    width: 60px !important;
    padding: 4px !important;
    text-align: center;
    height: 30px;
}

.unit {
    font-size: 0.8em;
    color: var(--text-muted);
}

/* Batch Mode Input */
.batch-input textarea {
    width: 100%;
    padding: 12px;
    background: var(--input-bg);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-md);
    color: var(--text-primary);
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.9em;
    resize: vertical;
}

.batch-actions {
    margin-top: 12px;
    display: flex;
    justify-content: flex-end;
}

/* Button & Spin Shared */
.btn-primary {
    padding: 0 24px;
    background: var(--accent-color);
    color: white;
    border: none;
    border-radius: var(--radius-md);
    font-weight: 500;
    display: flex;
    align-items: center;
    justify-content: center;
    min-width: 80px;
    height: 42px;
}
.btn-primary:hover { background: var(--accent-hover); }
.btn-primary:active { transform: translateY(1px); }
.btn-primary:disabled { opacity: 0.7; cursor: not-allowed; }

.spin { animation: spin 1s linear infinite; }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }


/* History */
.ping-history {
    flex: 1;
    overflow-y: auto;
    border: 1px solid var(--border-color);
    background: var(--panel-bg);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-sm);
}

.ping-item {
    display: flex;
    padding: 8px 16px;
    border-bottom: 1px solid var(--border-color);
    align-items: center;
}

.timestamp {
    color: var(--text-muted);
    font-size: 0.8em;
    margin-right: 15px;
    font-family: 'JetBrains Mono', monospace;
    width: 70px;
}

.domain {
    flex: 1;
    font-weight: 500;
    font-size: 1em;
    color: var(--text-primary);
}

.result-badge {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px;
    border-radius: 12px;
    font-size: 0.85em;
    font-weight: 600;
    background: rgba(34, 197, 94, 0.1);
    color: var(--success-color);
}
.result-badge.error {
    background: rgba(239, 68, 68, 0.1);
    color: var(--danger-color);
}

.empty-hint {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
    padding: 40px;
    opacity: 0.6;
}

/* Batch Styles */
.batch-run {
    border-bottom: 1px solid var(--border-color);
}
.batch-run:last-child { border-bottom: none; }

.batch-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 10px 12px;
    cursor: pointer;
    background: var(--panel-bg);
    transition: background 0.1s;
}
.batch-header:hover {
    background: var(--bg-color);
}

.batch-meta {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 0.9em;
    color: var(--text-primary);
}
.expand-icon { color: var(--text-muted); }
.icon-sm { color: var(--accent-color); }

.summary {
    font-weight: 500;
}

.time {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.9em;
    color: var(--text-muted);
}

.batch-results {
    background: var(--bg-color);
    border-top: 1px dashed var(--border-color);
}

.batch-item {
    padding-left: 38px !important; /* Indent batch items */
    font-size: 0.9em;
}

.batch-empty {
    padding: 10px 38px;
    font-size: 0.85em;
    color: var(--text-muted);
    font-style: italic;
}
</style>
