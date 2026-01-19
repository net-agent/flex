<template>
  <div class="flex-container">
    <header>
      <h1>Flex Web Client</h1>
      <div class="connection-status">
        <div class="indicator" :class="statusClass"></div>
        <span>{{ statusText }}</span>
      </div>
    </header>

    <div class="main-content">
      <!-- Connection Card -->
      <div class="card">
        <h2>Connection</h2>
        <div class="info-grid">
          <label>Gateway URL:</label>
          <input v-model="gatewayUrl" :disabled="isConnected" />

          <label>Domain:</label>
          <input v-model="connectDomain" placeholder="e.g. web-client-01" :disabled="isConnected" />

          <label>Password:</label>
          <input v-model="connectPassword" type="password" :disabled="isConnected" />
          
          <label>My Virtual IP:</label>
          <span class="value">{{ myIP || 'Unassigned' }}</span>

          <label>Heartbeat:</label>
          <span class="value">{{ heartbeatStatus }}</span>
        </div>
        
        <div class="actions">
          <button v-if="!isConnected" @click="connect" class="btn primary">Connect</button>
          <button v-else @click="disconnect" class="btn danger">Disconnect</button>
        </div>
      </div>

      <!-- Ping Tool Card -->
      <div class="card">
        <h2>Network Tools</h2>
        <div class="tool-row">
          <input v-model="pingTarget" placeholder="Target Domain (e.g. local)" @keyup.enter="sendPing" :disabled="!isReady" />
          <button @click="sendPing" :disabled="!isReady" class="btn secondary">Ping</button>
        </div>
        <div class="ping-result" v-if="lastPingResult">
          <span class="success-text" v-if="lastPingResult.latency >= 0">
            Reply from <strong>{{ lastPingResult.domain }}</strong>: time={{ lastPingResult.latency }}ms
          </span>
        </div>
      </div>

      <!-- Logs Card -->
      <div class="card logs-card">
        <div class="card-header">
           <h2>System Logs</h2>
           <button @click="clearLogs" class="btn small">Clear</button>
        </div>
        <div class="logs-window" ref="logsWindow">
          <div v-for="(log, idx) in logs" :key="idx" class="log-line">
            <span class="timestamp">{{ log.time }}</span>
            <span class="message">{{ log.msg }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, nextTick } from 'vue'
import { FlexNode } from './flex/node.js'

// State
const gatewayUrl = ref(`ws://${window.location.hostname}:8080/flex/ws`)
const connectDomain = ref(`web-${Math.floor(Math.random() * 10000)}`)
const connectPassword = ref('test-pwd')
const node = new FlexNode()
const connectionState = ref('disconnected') // disconnected, connecting, connected, ready
const myIP = ref(0)
const logs = ref([])
const pingTarget = ref('local')
const lastPingResult = ref(null)
const logsWindow = ref(null)

// Computed
const isConnected = computed(() => connectionState.value === 'connected' || connectionState.value === 'ready')
const isReady = computed(() => connectionState.value === 'ready')

const statusText = computed(() => {
  switch(connectionState.value) {
    case 'disconnected': return 'Disconnected';
    case 'connecting': return 'Connecting...';
    case 'handshaking': return 'Handshaking...';
    case 'connected': return 'Connected';
    case 'ready': return 'Online (Ready)';
    default: return 'Unknown';
  }
})

const statusClass = computed(() => {
  switch(connectionState.value) {
    case 'ready': return 'bg-green';
    case 'handshaking':
    case 'connected': return 'bg-yellow';
    default: return 'bg-red';
  }
})

const heartbeatStatus = computed(() => {
  return isReady.value ? 'Active (15s)' : 'Stopped';
})

// Methods
const addLog = (msg) => {
  const time = new Date().toLocaleTimeString();
  logs.value.push({ time, msg });
  if (logs.value.length > 100) logs.value.shift();
  
  nextTick(() => {
    if (logsWindow.value) {
      logsWindow.value.scrollTop = logsWindow.value.scrollHeight;
    }
  })
}

const clearLogs = () => logs.value = []

const connect = () => {
  if (!gatewayUrl.value) return;
  // Node.connect(url, password, domain) - we need to update node.js connect signature too
  node.connect(gatewayUrl.value, connectPassword.value, connectDomain.value);
}

const disconnect = () => {
  node.disconnect();
}

const sendPing = () => {
  if (!pingTarget.value) return;
  node.ping(pingTarget.value);
}

// Node Bindings
node.onLog = (msg) => addLog(msg)

node.onStateChange = (state) => {
  connectionState.value = state;
  if (state === 'ready') {
    myIP.value = node.ip;
  } else if (state === 'disconnected') {
    myIP.value = 0;
  }
}

node.onLatencyUpdate = (domain, ms) => {
  lastPingResult.value = { domain, latency: ms };
}

</script>

<style>
/* Global Reset & Font */
:root {
  --primary: #2563eb;
  --secondary: #475569;
  --danger: #dc2626;
  --bg: #f8fafc;
  --card-bg: #ffffff;
  --text: #1e293b;
  --border: #e2e8f0;
}

body {
  margin: 0;
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
  background-color: var(--bg);
  color: var(--text);
}

.flex-container {
  max-width: 900px;
  margin: 0 auto;
  padding: 2rem;
}

/* Header */
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 2rem;
}

.connection-status {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  background: white;
  padding: 0.5rem 1rem;
  border-radius: 20px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.1);
  font-weight: 500;
  font-size: 0.9rem;
}

.indicator {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}
.bg-green { background-color: #10b981; }
.bg-yellow { background-color: #f59e0b; }
.bg-red { background-color: #ef4444; }

/* Grid & Cards */
.main-content {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
}

.card {
  background: var(--card-bg);
  padding: 1.5rem;
  border-radius: 12px;
  box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1);
  border: 1px solid var(--border);
}

.card h2 {
  margin-top: 0;
  font-size: 1.25rem;
  margin-bottom: 1.5rem;
  border-bottom: 1px solid var(--border);
  padding-bottom: 0.75rem;
}

/* Forms & Inputs */
.info-grid {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 0.75rem 1.5rem;
  align-items: center;
}

label {
  color: var(--secondary);
  font-size: 0.9rem;
  font-weight: 500;
}

input {
  width: 100%;
  padding: 0.6rem;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 1rem;
  box-sizing: border-box;
}

input:disabled {
  background-color: #f1f5f9;
}

.value {
  font-family: monospace;
  font-weight: 600;
}

.actions {
  margin-top: 1.5rem;
  display: flex;
  justify-content: flex-end;
}

/* Buttons */
.btn {
  padding: 0.6rem 1.2rem;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  font-weight: 600;
  transition: opacity 0.2s;
}
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn.primary { background: var(--primary); color: white; }
.btn.danger { background: var(--danger); color: white; }
.btn.secondary { background: var(--secondary); color: white; }
.btn.small { padding: 0.3rem 0.8rem; font-size: 0.85rem; }

/* Ping Area */
.tool-row {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.ping-result {
  margin-top: 1rem;
  padding: 1rem;
  background: #f0fdf4;
  border-radius: 6px;
  color: #15803d;
  font-size: 0.95rem;
}

/* Logs */
.logs-card {
  grid-column: 1 / -1;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid var(--border);
  margin-bottom: 1rem;
  padding-bottom: 0.5rem;
}
.card-header h2 { margin: 0; border: none; padding: 0; }

.logs-window {
  height: 250px;
  overflow-y: auto;
  background: #1e293b;
  border-radius: 8px;
  padding: 1rem;
  color: #e2e8f0;
  font-family: 'Fira Code', monospace;
  font-size: 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.timestamp {
  color: #94a3b8;
  margin-right: 0.75rem;
  font-size: 0.75rem;
}
</style>
