<template>
    <div class="monitor-panel">
        <div class="config-toggle" @click="showConfig = !showConfig">
             <Settings :size="16" />
             <span>{{ showConfig ? 'Hide Config' : 'Show Config' }}</span>
        </div>

        <div class="config-section" v-if="showConfig || targets.length === 0">
            <div class="input-group">
                 <div class="input-wrapper">
                    <label>Monitoring Targets</label>
                    <textarea 
                        v-model="targetListRaw" 
                        placeholder="Enter domains to monitor, one per line..." 
                        rows="4"
                        :disabled="isRunning"
                        class="monitor-textarea"
                    ></textarea>
                </div>
            </div>

            <div class="settings-row">
                <div class="setting-item">
                    <label>Interval (s)</label>
                    <input 
                        v-model.number="intervalSeconds" 
                        type="number" 
                        min="1" 
                        class="flex-input sm" 
                        :disabled="isRunning"
                    />
                </div>
                
                <div class="setting-item flex-1">
                    <label>Webhook URL (General)</label>
                    <input 
                        v-model="webhookUrl" 
                        type="text" 
                        placeholder="https://api.example.com/notify" 
                        class="flex-input" 
                        :disabled="isRunning"
                    />
                </div>
            </div>

            <div class="settings-row">
                 <div class="setting-item flex-1">
                    <label>WeCom Robot Key (Optional)</label>
                    <input 
                        v-model="wecomKey" 
                        type="password" 
                        placeholder="Enter key (e.g. 693a...)" 
                        class="flex-input" 
                        :disabled="isRunning"
                        autocomplete="off"
                    />
                </div>
            </div>

            <!-- Permission Check -->
            <div class="permission-row">
                <div class="perm-status" v-if="permissionStatus === 'granted'">
                     <CheckCircle2 :size="14" class="text-success" />
                     <span>Desktop Notifications Enabled</span>
                     <button @click="testNotify" class="link-btn">Test</button>
                </div>
                <div class="perm-status" v-else>
                     <AlertTriangle :size="14" class="text-warning" />
                     <span>Notifications: {{ permissionStatus }}</span>
                     <button @click="requestPerm" class="link-btn">Enable</button>
                </div>
            </div>

            <div class="actions">
                <button class="btn-primary" @click="toggleMonitor">
                    <span v-if="!isRunning">Start Monitor</span>
                    <span v-else>Stop Monitor</span>
                </button>
            </div>
        </div>

        <div class="monitor-status" v-if="targets.length > 0">
            <div class="status-header">
                <span>System Status</span>
                <span class="pulse" v-if="isRunning">● Monitoring</span>
            </div>
            
            <div class="status-list">
                <div v-for="t in targets" :key="t.domain" class="status-row">
                    <!-- Top Row: Icon, Name, Uptime -->
                    <div class="row-header">
                        <div class="left-col">
                            <component :is="getStatusIcon(t.status)" :size="18" :class="['status-icon', t.status]" />
                            <span class="domain-name">{{ t.domain }}</span>
                            <span class="latency" v-if="t.status === 'alive'">{{ t.latency }}ms</span>
                        </div>
                        <div class="right-col">
                            <span class="uptime-label">{{ t.uptime }}% uptime</span>
                        </div>
                    </div>

                    <!-- Bottom Row: Tick Bar -->
                    <div class="uptime-bar">
                        <div 
                            v-for="(tick, idx) in t.history" 
                            :key="idx" 
                            :class="['tick', tick.status]"
                            :title="tick.title"
                        ></div>
                        <!-- Fill remaining with empty ticks if history < MAX -->
                        <div v-for="i in (maxHistory - t.history.length)" :key="'empty'+i" class="tick empty"></div>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, computed, onUnmounted, onMounted, watch } from 'vue';
import { flexService } from '../../services/flex.js';
import { CheckCircle2, AlertTriangle, HelpCircle, XCircle, Settings } from 'lucide-vue-next';

// State
const showConfig = ref(true);
const targetListRaw = ref('');
const intervalSeconds = ref(5);
const webhookUrl = ref('');
const wecomKey = ref('');
const isRunning = ref(false);
const targets = ref([]); 
const permissionStatus = ref('default');

const maxHistory = 60;
let timerId = null;

// Helpers
const getStatusIcon = (status) => {
    switch(status) {
        case 'alive': return CheckCircle2;
        case 'dead': return XCircle;
        default: return HelpCircle;
    }
};

onMounted(() => {
    checkPermission();
});

const checkPermission = () => {
    if ('Notification' in window) {
        permissionStatus.value = Notification.permission;
    }
};

const requestPerm = async () => {
    if ('Notification' in window) {
        const p = await Notification.requestPermission();
        permissionStatus.value = p;
    }
};

const testNotify = () => {
    // 1. Browser
    if ('Notification' in window && Notification.permission === 'granted') {
        new Notification("Flex Monitor", { body: "Test Notification", icon: '/favicon.ico' });
    } else {
        alert("Desktop Notifications not granted!");
    }
    
    // 2. WeCom Test
    if (wecomKey.value) {
        triggerWecom("Test Notification from Flex Monitor");
    }
};

// Logic
const toggleMonitor = () => {
    if (isRunning.value) {
        stopMonitor();
    } else {
        startMonitor();
    }
};

const startMonitor = async () => {
    // Parse targets
    const list = targetListRaw.value.split('\n').map(d => d.trim()).filter(d => d.length > 0);
    if (list.length === 0) return;

    if ('Notification' in window && Notification.permission !== 'granted') {
        await Notification.requestPermission();
    }

    // Init State
    targets.value = list.map(d => ({
        domain: d,
        status: 'pending',
        previousStatus: null,
        latency: null,
        error: null,
        lastCheck: null,
        history: [], // Start empty
        uptime: 100.00
    }));

    isRunning.value = true;
    showConfig.value = false; // Auto hide config
    runCheckCycle();
    timerId = setInterval(runCheckCycle, intervalSeconds.value * 1000);
};

const stopMonitor = () => {
    isRunning.value = false;
    showConfig.value = true;
    if (timerId) {
        clearInterval(timerId);
        timerId = null;
    }
};

const runCheckCycle = async () => {
    if (!isRunning.value) return;

    const promises = targets.value.map(async (t) => {
        const now = new Date().toLocaleTimeString();
        let currentStatus = 'dead';
        let latency = 0;
        let title = `${now}: Timeout`;

        try {
            const ms = await flexService.ping(t.domain);
            currentStatus = 'alive';
            latency = ms;
            title = `${now}: ${ms}ms`;
        } catch (e) {
            currentStatus = 'dead';
            title = `${now}: ${e.message}`;
        }

        // 1. Notification Logic
        if (t.previousStatus && t.previousStatus !== currentStatus) {
            triggerNotification(t, currentStatus);
        }

        // 2. Update Current State
        t.status = currentStatus;
        t.latency = latency;
        t.previousStatus = currentStatus;
        
        // 3. Update History
        const tick = { status: currentStatus, title };
        t.history.push(tick);
        if (t.history.length > maxHistory) {
            t.history.shift();
        }

        // 4. Calculate Uptime
        const total = t.history.length;
        const alive = t.history.filter(x => x.status === 'alive').length;
        t.uptime = total > 0 ? ((alive / total) * 100).toFixed(2) : 100.00;
    });

    await Promise.all(promises);
};

// Notification
const triggerNotification = (target, newStatus) => {
    const statusText = newStatus === 'alive' ? 'UP' : 'DOWN';
    
    // 1. Browser
    if ('Notification' in window && Notification.permission === 'granted') {
        const body = newStatus === 'alive' 
            ? `Target is UP (Latency: ${target.latency}ms)` 
            : `Target is DOWN`;
        new Notification(`Monitor Alert: ${target.domain}`, { body, icon: '/favicon.ico' });
    }

    // 2. WeCom
    if (wecomKey.value) {
        const content = `[Monitor Alert] ${target.domain} is ${statusText}\nTime: ${new Date().toLocaleTimeString()}\nDetails: ${newStatus === 'alive' ? target.latency + 'ms' : target.error}`;
        triggerWecom(content);
    }

    // 3. Generic Webhook
    if (webhookUrl.value) {
        fetch(webhookUrl.value, {
            method: 'POST',
            body: JSON.stringify({
                event: 'status_change',
                domain: target.domain,
                status: newStatus,
                timestamp: Date.now()
            })
        }).catch(console.error);
    }
};

const triggerWecom = (content) => {
    const url = `https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=${wecomKey.value}`;
    fetch(url, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            msgtype: "text",
            text: {
                content: content
            }
        })
    }).catch(e => console.error("WeCom Webhook Failed", e));
};

onUnmounted(() => {
    stopMonitor();
});

</script>

<style scoped>
.monitor-panel {
    display: flex;
    flex-direction: column;
    gap: 16px;
    height: 100%;
}

.config-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.85em;
    color: var(--text-secondary);
    cursor: pointer;
    user-select: none;
    align-self: flex-end;
}
.config-toggle:hover { color: var(--text-primary); }

.config-section {
    background: var(--panel-bg);
    padding: 20px;
    border: 1px solid var(--border-color);
    border-radius: var(--radius-md);
    display: flex;
    flex-direction: column;
    gap: 16px;
}

.monitor-textarea {
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

.settings-row {
    display: flex;
    gap: 16px;
    align-items: flex-end;
}

.setting-item {
    display: flex;
    flex-direction: column;
    gap: 6px;
}
.setting-item label {
    font-size: 0.85em;
    color: var(--text-secondary);
}

.flex-1 { flex: 1; }

.flex-input {
    padding: 8px 12px;
    background: var(--input-bg);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-md);
    color: var(--text-primary);
}
.flex-input.sm { width: 80px; }

.permission-row {
    font-size: 0.85em;
    display: flex;
    align-items: center;
    gap: 10px;
    background: var(--bg-color);
    padding: 8px 12px;
    border-radius: var(--radius-sm);
    border: 1px dashed var(--border-color);
}

.perm-status {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--text-secondary);
}

.text-success { color: var(--success-color); }
.text-warning { color: var(--accent-color); }

.link-btn {
    background: none;
    border: none;
    color: var(--accent-color);
    text-decoration: underline;
    cursor: pointer;
    font-size: inherit;
    padding: 0;
}
.link-btn:hover { color: var(--accent-hover); }

.btn-primary {
    width: 100%;
    padding: 10px;
    background: var(--accent-color);
    color: white;
    border: none;
    border-radius: var(--radius-md);
    font-weight: 600;
    cursor: pointer;
}

/* Status List Styles */
.monitor-status {
    flex: 1;
    overflow-y: auto;
    background: var(--bg-color); /* Matches card bg */
    border: 1px solid var(--border-color);
    border-radius: var(--radius-lg);
    padding: 20px;
}

.status-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 24px;
    font-size: 1.1em;
    font-weight: 600;
}

.status-list {
    display: flex;
    flex-direction: column;
    gap: 32px;
}

.status-row {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.row-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
}

.left-col {
    display: flex;
    align-items: center;
    gap: 10px;
}

.status-icon.alive { color: var(--success-color); }
.status-icon.dead { color: var(--danger-color); }
.status-icon.pending { color: var(--text-muted); }

.domain-name {
    font-weight: 600;
    font-size: 1em;
}

.latency {
    font-size: 0.85em;
    color: var(--text-secondary);
    font-family: 'JetBrains Mono', monospace;
}

.uptime-label {
    font-size: 0.9em;
    color: var(--text-muted);
}

/* Uptime Bar */
.uptime-bar {
    display: flex;
    gap: 3px;
    height: 32px;
}

.tick {
    flex: 1;
    background: var(--border-color); /* Default/Empty */
    border-radius: 2px;
    transition: all 0.2s;
    cursor: help;
}
.tick.alive { background: var(--success-color); }
.tick.dead { background: var(--danger-color); }
.tick.empty { background: var(--input-bg); opacity: 0.5; }

.tick:hover {
    transform: scaleY(1.2);
    opacity: 0.9;
}

.pulse {
    color: var(--success-color);
    font-size: 0.8em;
    animation: pulse 1.5s infinite;
}
@keyframes pulse { 0% { opacity: 1; } 50% { opacity: 0.5; } 100% { opacity: 1; } }
</style>
