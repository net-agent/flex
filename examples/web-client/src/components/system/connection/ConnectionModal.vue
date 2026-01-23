<template>
  <Transition name="fade">
  <div class="modal-overlay" v-if="isVisible">
    <div class="modal-content">
      <div class="modal-header">
        <div class="title-group">
            <Network :size="24" class="icon" />
            <h3>Enter Flex Network</h3>
        </div>
        <button class="close-btn" @click="close">
            <X :size="20" />
        </button>
      </div>
      
      <ConnectionForm
          v-model:gatewayUrl="gatewayUrl"
          v-model:domain="domain"
          v-model:password="password"
          :isConnected="isConnected"
          :isBusy="isConnecting"
          :status="connectionState"
          @generate="generateDomain"
          @connect="handleConnect"
          @disconnect="handleDisconnect"
      />
    </div>
  </div>
  </Transition>
</template>

<script setup>
import { ref, computed, watch, onMounted } from 'vue';
import { flexService } from '../../../services/flex.js'; // Updated path
import { Network, X } from 'lucide-vue-next';
import ConnectionForm from './ConnectionForm.vue';

const props = defineProps({
    modelValue: Boolean
});

const emit = defineEmits(['update:modelValue']);

// Local State
const gatewayUrl = ref('');
const domain = ref('');
const password = ref('test-pwd');

// Service State
const connectionState = computed(() => flexService.state.connectionState);

// Derived State
const isConnected = computed(() => connectionState.value === 'ready');
const isConnecting = computed(() => connectionState.value === 'connecting' || connectionState.value === 'handshaking');
const isVisible = computed(() => props.modelValue);

const STORAGE_KEY_DOMAIN = 'flex_client_domain';

onMounted(() => {
    // Dynamic Gateway URL
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    gatewayUrl.value = `${protocol}//${window.location.host}/flex/ws`;

    const savedDomain = localStorage.getItem(STORAGE_KEY_DOMAIN);
    if (savedDomain) domain.value = savedDomain;
});

watch(domain, (newVal) => {
    if (newVal) localStorage.setItem(STORAGE_KEY_DOMAIN, newVal);
});

const close = () => {
    emit('update:modelValue', false);
};

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
.modal-overlay {
    position: fixed;
    top: 0; left: 0; right: 0; bottom: 0;
    background: rgba(0,0,0,0.5);
    display: flex;
    justify-content: center;
    align-items: center;
    z-index: 1000;
    backdrop-filter: blur(4px);
}

.modal-content {
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    box-shadow: var(--shadow-xl);
    padding: 32px;
    border-radius: var(--radius-lg);
    width: 420px;
    max-width: 90vw;
}

.modal-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 24px;
}

.title-group {
    display: flex;
    align-items: center;
    gap: 10px;
}

.title-group .icon {
    color: var(--accent-color);
}

.modal-header h3 {
    margin: 0;
    color: var(--text-primary);
    font-weight: 600;
}

.close-btn {
    background: none;
    border: none;
    cursor: pointer;
    color: var(--text-muted);
    padding: 4px;
    border-radius: var(--radius-sm);
}
.close-btn:hover { background: var(--bg-color); color: var(--text-primary); }

.form-group {
    margin-bottom: 20px;
}

.form-group label {
    display: block;
    margin-bottom: 8px;
    font-size: 0.9em;
    font-weight: 500;
    color: var(--text-secondary);
}

.input-wrapper {
    position: relative;
    display: flex;
    align-items: center;
}

.input-icon {
    position: absolute;
    left: 12px;
    color: var(--text-muted);
}

.form-group input {
    width: 100%;
    padding: 10px 10px 10px 38px;
    border-radius: var(--radius-md);
    border: 1px solid var(--input-border);
    background: var(--input-bg);
    color: var(--text-primary);
    font-size: 0.95em;
}

.input-row {
    display: flex;
    gap: 10px;
}

.flex-1 { flex: 1; }

.icon-btn-secondary {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 42px;
    background: var(--input-bg);
    border: 1px solid var(--input-border);
    border-radius: var(--radius-md);
    color: var(--text-secondary);
}
.icon-btn-secondary:hover {
    border-color: var(--accent-color);
    color: var(--accent-color);
}

.modal-actions {
    margin-top: 30px;
}

.btn-primary {
    width: 100%;
    padding: 12px;
    background: var(--accent-color);
    color: white;
    border: none;
    border-radius: var(--radius-md);
    cursor: pointer;
    font-weight: 600;
    font-size: 1em;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
}
.btn-primary:hover { background: var(--accent-hover); box-shadow: var(--shadow-md); }
.btn-primary:active { transform: translateY(1px); }

.btn-danger {
    width: 100%;
    padding: 12px;
    background: var(--danger-color);
    color: white;
    border: none;
    border-radius: var(--radius-md);
    cursor: pointer;
    font-weight: 600;
    font-size: 1em;
}
.btn-danger:hover { opacity: 0.9; }

.status-msg {
    margin-top: 20px;
    display: flex;
    justify-content: center;
    align-items: center;
    gap: 8px;
    font-size: 0.9em;
    color: var(--text-secondary);
}

.spin {
    animation: spin 1s linear infinite;
}
@keyframes spin {
    from { transform: rotate(0deg); }
    to { transform: rotate(360deg); }
}
</style>
