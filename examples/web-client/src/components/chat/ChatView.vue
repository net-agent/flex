<template>
  <div class="chat-tool-container">
    <div class="tool-header">
        <div class="tabs">
            <button 
                :class="{ active: activeTab === 'client' }" 
                @click="activeTab = 'client'"
            >
                Chat Client
            </button>
            <button 
                :class="{ active: activeTab === 'server' }" 
                @click="activeTab = 'server'"
            >
                Chat Server
            </button>
        </div>
    </div>

    <div class="tool-content">
        <ChatClientPanel v-if="activeTab === 'client'" />
        <ChatServerPanel v-if="activeTab === 'server'" />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue';
import { flexService } from '../../services/flex.js';
import { chatClientManager } from '../../flex/chatroom/client-manager.js';
import { chatServerManager } from '../../flex/chatroom/server-manager.js';

import ChatClientPanel from './ChatClientPanel.vue';
import ChatServerPanel from './ChatServerPanel.vue';

const activeTab = ref('client');

onMounted(() => {
    // Initialize both managers
    chatServerManager.init(flexService.node);
    chatClientManager.init(flexService.node);

    // Auto-Restore Client Sessions
    chatClientManager.restoreSessions();
});
</script>

<style scoped>
.chat-tool-container {
    display: flex;
    flex-direction: column;
    height: 100%;
    width: 100%;
    background: var(--bg-color);
}

.tool-header {
    flex: 0 0 auto;
    background: var(--bg-darker);
    border-bottom: 1px solid var(--border-color);
    padding: 0 16px;
    display: flex;
    align-items: center;
    height: 48px;
}

.tabs {
    display: flex;
    gap: 8px;
    height: 100%;
}

.tabs button {
    background: transparent;
    border: none;
    color: var(--text-muted);
    padding: 0 16px;
    cursor: pointer;
    font-weight: 500;
    height: 100%;
    border-bottom: 2px solid transparent;
    transition: all 0.2s;
}

.tabs button.active {
    color: var(--text-primary);
    border-bottom-color: var(--accent-color);
}
.tabs button:hover:not(.active) {
    color: var(--text-secondary);
}

.tool-content {
    flex: 1;
    overflow: hidden;
    position: relative;
}
</style>
