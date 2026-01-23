<template>
  <div class="connection-form">
      <div class="form-group">
        <label>Gateway URL</label>
        <div class="input-wrapper">
            <input 
                :value="gatewayUrl" 
                @input="$emit('update:gatewayUrl', $event.target.value)"
                type="text" 
                placeholder="ws://localhost:9090/flex/ws" 
                :disabled="isConnected" 
            />
        </div>
      </div>

      <div class="form-group">
        <label>Domain</label>
        <div class="input-row">
            <div class="input-wrapper flex-1">
                <input 
                    :value="domain" 
                    @input="$emit('update:domain', $event.target.value)"
                    type="text" 
                    placeholder="auto-generated" 
                    :disabled="isConnected" 
                />
            </div>
            <button class="icon-btn-secondary" @click="$emit('generate')" :disabled="isConnected" title="Generate Random">
                🎲
            </button>
        </div>
      </div>

      <div class="form-group">
        <label>Password</label>
        <div class="input-row">
            <div class="input-wrapper flex-1">
                <input 
                    :value="password" 
                    @input="$emit('update:password', $event.target.value)"
                    :type="showPassword ? 'text' : 'password'" 
                    :disabled="isConnected" 
                />
            </div>
            <button class="icon-btn-secondary" @click="showPassword = !showPassword" :title="showPassword ? 'Hide Password' : 'Show Password'">
                <EyeOff v-if="showPassword" :size="18" />
                <Eye v-else :size="18" />
            </button>
        </div>
      </div>

      <div class="actions">
        <template v-if="!isConnected">
            <div v-if="isBusy" class="busy-actions">
                <button class="btn-primary" disabled>
                    <span class="spin icon-spacer">⟳</span> Connecting...
                </button>
                <button @click="$emit('disconnect')" class="btn-cancel" title="Cancel Connection">
                    Cancel
                </button>
            </div>
            <button v-else @click="$emit('connect')" class="btn-primary">
                Connect
            </button>
        </template>
        
        <button v-else @click="$emit('disconnect')" class="btn-danger">
          Disconnect
        </button>
      </div>
      
      <div class="status-msg">
        <span :class="{ 'text-transparent': !status || status === 'disconnected' }">{{ status || 'Ready' }}</span>
      </div>
  </div>
</template>

<script setup>
import { ref } from 'vue';
import { Eye, EyeOff } from 'lucide-vue-next';

defineProps({
    gatewayUrl: String,
    domain: String,
    password: String,
    isConnected: Boolean,
    isBusy: Boolean,
    status: String
});

defineEmits(['update:gatewayUrl', 'update:domain', 'update:password', 'generate', 'connect', 'disconnect']);

const showPassword = ref(false);
</script>

<style scoped>
.form-group {
    margin-bottom: 16px;
}
.form-group label {
    display: block;
    margin-bottom: 6px;
    font-size: 0.85em;
    color: var(--text-secondary);
}
.input-wrapper input {
    width: 100%;
    padding: 10px;
    background: var(--input-bg);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    color: var(--text-primary);
}
.input-row {
    display: flex;
    gap: 8px;
}
.flex-1 { flex: 1; }

.icon-btn-secondary {
    padding: 0 12px;
    background: var(--bg-hover);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    cursor: pointer;
}

.actions {
    margin-top: 24px;
}

.busy-actions {
    display: flex;
    gap: 8px;
    width: 100%;
}

.btn-primary {
    width: 100%;
    padding: 10px;
    background: var(--accent-color);
    color: white;
    border: none;
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
}
.btn-primary:disabled {
    opacity: 0.7;
    cursor: not-allowed;
}

.btn-cancel {
    padding: 0 12px;
    background: var(--bg-hover);
    border: 1px solid var(--border-color);
    color: var(--text-primary);
    border-radius: 6px;
    cursor: pointer;
    font-weight: 500;
}
.btn-cancel:hover {
    background: var(--input-bg);
    border-color: var(--danger-color);
    color: var(--danger-color);
}

.btn-danger {
    width: 100%;
    padding: 10px;
    background: var(--error-color);
    color: white;
    border: none;
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
}

.status-msg {
    margin-top: 16px;
    text-align: center;
    font-size: 0.9em;
    color: var(--text-muted);
    min-height: 20px; /* Prevent layout shift */
    display: flex;
    justify-content: center;
    align-items: center;
}

.text-transparent {
    color: transparent;
    user-select: none;
}

.spin { display: inline-block; animation: spin 1s linear infinite; }
@keyframes spin { 100% { transform: rotate(360deg); } }
.icon-spacer { margin-right: 6px; }
</style>
