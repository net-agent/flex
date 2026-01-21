<template>
  <div class="panel connection-panel">
    <h3>Connection</h3>
    
    <div class="form-group">
      <label>Gateway URL:</label>
      <input v-model="gatewayUrl" type="text" placeholder="ws://localhost:9090/flex/ws" :disabled="isConnected" />
    </div>

    <div class="form-group">
      <label>Domain (Optional):</label>
      <div class="input-row">
        <input v-model="domain" type="text" placeholder="auto-generated" :disabled="isConnected" />
        <button class="small-btn" @click="generateDomain" :disabled="isConnected">🎲</button>
      </div>
    </div>

    <div class="form-group">
      <label>Password:</label>
      <input v-model="password" type="password" placeholder="Gateway Password" :disabled="isConnected" />
    </div>

    <div class="actions">
      <button v-if="!isConnected" @click="handleConnect" class="btn-primary" :disabled="isConnecting">
        {{ isConnecting ? 'Connecting...' : 'Connect' }}
      </button>
      <button v-else @click="handleDisconnect" class="btn-danger">
        Disconnect
      </button>
    </div>

    <div class="status-indicator">
      Status: <span :class="['status-tag', connectionState]">{{ connectionState }}</span>
    </div>
    
    <div v-if="isConnected" class="info-block">
        <div><strong>IP:</strong> {{ ip }}</div>
        <div><strong>Domain:</strong> {{ activeDomain }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted } from 'vue';
import { flexService } from '../services/flex.js';

// Local State
const gatewayUrl = ref('ws://localhost:9090/flex/ws');
const domain = ref('');
const password = ref('test-pwd');

// Service State
const connectionState = computed(() => flexService.state.connectionState);
const ip = computed(() => flexService.state.ip);
const activeDomain = computed(() => flexService.state.domain);

// Derived State
const isConnected = computed(() => connectionState.value === 'ready');
const isConnecting = computed(() => connectionState.value === 'connecting' || connectionState.value === 'handshaking');

// Persistence
const STORAGE_KEY_DOMAIN = 'flex_client_domain';

onMounted(() => {
    // Load persisted domain preference
    const savedDomain = localStorage.getItem(STORAGE_KEY_DOMAIN);
    if (savedDomain) {
        domain.value = savedDomain;
    }
    // Update service domain immediately if we have one, so generated domain logic works if empty logic is applied there?
    // Actually service handles logic. But if we want to pass a stored domain, we should align.
});

watch(domain, (newVal) => {
    if (newVal) {
        localStorage.setItem(STORAGE_KEY_DOMAIN, newVal);
    }
});

watch(activeDomain, (newVal) => {
    // If service updates domain (e.g. auto-generated), Sync back to UI if strictly needed,
    // or just let the info-block show it.
    // If user didn't type anything, maybe we want to show it in the input? 
    // Let's keep input for "Requesting" and info-block for "Actual".
    if (!domain.value && newVal) {
        // If user input was empty, maybe we don't auto-fill to avoid confusion between "I typed this" vs "It was assigned".
        // functionality-wise, we persist what user *intends*. 
    }
});

// Actions
const generateDomain = () => {
    domain.value = "web-" + Math.random().toString(36).substr(2, 6);
};

const handleConnect = () => {
    flexService.connect(gatewayUrl.value, password.value, domain.value);
};

const handleDisconnect = () => {
    flexService.disconnect();
};
</script>

<style scoped>
.panel {
  background: #252526;
  padding: 15px;
  border-radius: 6px;
  border: 1px solid #3e3e42;
}

.form-group {
  margin-bottom: 12px;
}

.form-group label {
  display: block;
  margin-bottom: 5px;
  font-size: 0.9em;
  color: #cccccc;
}

.form-group input {
  width: 100%;
  padding: 8px;
  background: #1e1e1e;
  border: 1px solid #3e3e42;
  color: white;
  border-radius: 4px;
}

.input-row {
    display: flex;
    gap: 5px;
}

.small-btn {
    padding: 0 10px;
    background: #3e3e42;
    border: none;
    color: white;
    cursor: pointer;
    border-radius: 4px;
}

.actions {
  margin-top: 20px;
}

.btn-primary {
  width: 100%;
  padding: 10px;
  background: #007acc;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-weight: bold;
}

.btn-primary:hover {
  background: #0098ff;
}

.btn-primary:disabled {
  background: #444;
  cursor: not-allowed;
}

.btn-danger {
  width: 100%;
  padding: 10px;
  background: #ce3838;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-weight: bold;
}

.status-indicator {
    margin-top: 15px;
    font-size: 0.9em;
    color: #888;
}

.status-tag {
    color: white;
    padding: 2px 6px;
    border-radius: 4px;
    font-size: 0.85em;
    text-transform: uppercase;
}
.status-tag.disconnected { background: #555; }
.status-tag.connecting { background: #dda0dd; color: black; }
.status-tag.handshaking { background: #ffa500; color: black; }
.status-tag.ready { background: #4caf50; }

.info-block {
    margin-top: 15px;
    padding: 10px;
    background: #1e1e1e;
    border-radius: 4px;
    font-size: 0.9em;
}
</style>
