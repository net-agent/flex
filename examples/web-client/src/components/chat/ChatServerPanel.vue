<template>
  <div class="chat-server-panel">
    <div class="server-header">
        <h3>Server Management</h3>
        <span class="status-badge" :class="{ active: serverManager.state.isRunning }">
            {{ serverManager.state.isRunning ? 'Running' : 'Stopped' }}
        </span>
    </div>

    <div class="config-section">
        <div class="form-group">
            <label>Room Name</label>
            <input 
                v-model="configName" 
                type="text" 
                :disabled="serverManager.state.isRunning"
                placeholder="My Chat Room"
            />
        </div>
        
        <div class="actions">
            <button 
                class="btn-primary" 
                v-if="!serverManager.state.isRunning" 
                @click="startServer"
                :disabled="isBusy"
            >
                {{ isBusy ? 'Starting...' : 'Start Server' }}
            </button>
            <button 
                class="btn-danger" 
                v-else 
                @click="stopServer"
            >
                Stop Server
            </button>
        </div>
    </div>

    <!-- Connected Users (Monitor) -->
    <div class="users-section" v-if="serverManager.state.isRunning">
        <h4>Connected Users ({{ serverManager.state.users.length }})</h4>
        <ul class="user-list">
            <li v-for="u in serverManager.state.users" :key="u.id">
                {{ u.nickname }} <span class="sub">({{ u.domain }})</span>
            </li>
            <li v-if="serverManager.state.users.length === 0" class="empty">
                No users connected
            </li>
        </ul>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue';
import { chatServerManager } from '../../flex/chatroom/server-manager.js';
import { flexService } from '../../services/flex.js';

const serverManager = chatServerManager;
const isBusy = ref(false);

const configName = ref(`${flexService.state.domain}'s chat room`);

const startServer = async () => {
    isBusy.value = true;
    const nick = flexService.state.domain || 'Host';
    const success = await serverManager.startServer(configName.value, nick);
    if (!success) alert("Failed to start server");
    isBusy.value = false;
};

const stopServer = () => {
    serverManager.stopServer();
};
</script>

<style scoped>
.chat-server-panel {
    padding: 24px;
    height: 100%;
    overflow-y: auto;
    color: var(--text-primary);
}

.server-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 32px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border-color);
}
.server-header h3 {
    margin: 0;
}

.status-badge {
    padding: 4px 12px;
    border-radius: 12px;
    background: var(--bg-darker);
    color: var(--text-muted);
    font-size: 0.85em;
    font-weight: 600;
}
.status-badge.active {
    background: rgba(16, 185, 129, 0.1); /* Green tint */
    color: var(--success-color);
}

.config-section {
    background: var(--panel-bg);
    padding: 20px;
    border-radius: 8px;
    border: 1px solid var(--border-color);
    margin-bottom: 24px;
}

.form-group {
    margin-bottom: 16px;
}
.form-group label {
    display: block;
    margin-bottom: 8px;
    color: var(--text-secondary);
    font-size: 0.9em;
}
.form-group input {
    width: 100%;
    padding: 10px;
    background: var(--input-bg);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    color: var(--text-primary);
}
.form-group input:disabled {
    opacity: 0.7;
    cursor: not-allowed;
}

.actions {
    display: flex;
    gap: 12px;
}

.btn-primary {
    background: var(--accent-color);
    color: white;
    padding: 10px 20px;
    border: none;
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
}
.btn-primary:hover {
    filter: brightness(1.1);
}
.btn-danger {
    background: rgba(239, 68, 68, 0.1);
    color: var(--error-color);
    border: 1px solid var(--error-color);
    padding: 10px 20px;
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
}
.btn-danger:hover {
    background: rgba(239, 68, 68, 0.2);
}

.users-section h4 {
    margin-bottom: 16px;
    color: var(--text-secondary);
}
.user-list {
    list-style: none;
    padding: 0;
    margin: 0;
    background: var(--panel-bg);
    border-radius: 8px;
    overflow: hidden;
}
.user-list li {
    padding: 12px 16px;
    border-bottom: 1px solid var(--border-color);
    font-size: 0.95em;
}
.user-list li:last-child {
    border-bottom: none;
}
.user-list li.empty {
    color: var(--text-muted);
    font-style: italic;
    text-align: center;
    padding: 20px;
}
.sub {
    color: var(--text-muted);
    font-size: 0.85em;
    margin-left: 6px;
}
</style>
