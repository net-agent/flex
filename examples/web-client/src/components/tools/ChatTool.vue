<template>
  <div class="chat-tool-v2">
    <!-- Sidebar -->
    <ChatSidebar 
        :sessions="manager.state.sessions" 
        :activeId="manager.state.activeSessionId" 
        @select="selectSession"
        @add="showAddModal = true"
    />

    <!-- Main Content -->
    <div class="chat-main" v-if="activeSession">
        <ChatWindow 
            :session="activeSession" 
            @send="sendMessage"
            @leave="leaveSession"
            @toggleUsers="showUserList = !showUserList"
        />
        
        <!-- User List Panel -->
        <UserList 
            v-if="showUserList" 
            :users="activeSession.users" 
            @close="showUserList = false" 
        />
    </div>

    <div class="empty-state" v-else>
        <div class="empty-content">
            <h3>No Chat Selected</h3>
            <button class="btn-primary" @click="showAddModal = true">Join or Host a Room</button>
        </div>
    </div>

    <!-- Add Room Modal -->
    <div v-if="showAddModal" class="modal-overlay" @click.self="showAddModal = false">
        <div class="modal">
            <h3>Add Room</h3>
            <div class="tabs">
                <button :class="{ active: modalMode === 'join' }" @click="modalMode = 'join'">Join</button>
                <button :class="{ active: modalMode === 'host' }" @click="modalMode = 'host'">Host</button>
            </div>
            
            <div class="modal-body">
                <div class="form-group">
                    <label>Nickname</label>
                    <input v-model="formNickname" ref="nicknameInput" type="text" placeholder="Your Name" />
                </div>
                
                <template v-if="modalMode === 'join'">
                    <div class="form-group">
                        <label>Target Domain/IP</label>
                        <input v-model="formDomain" type="text" placeholder="web-xxxxx" />
                    </div>
                </template>
                
                <template v-if="modalMode === 'host'">
                     <div class="form-group">
                        <label>Room Name</label>
                        <input v-model="formName" type="text" placeholder="My Chat Room" />
                    </div>
                </template>
            </div>

            <div class="modal-actions">
                <button class="btn-cancel" @click="showAddModal = false">Cancel</button>
                <button class="btn-primary" @click="confirmAdd" :disabled="isBusy">
                    {{ isBusy ? 'Working...' : (modalMode === 'join' ? 'Join' : 'Create') }}
                </button>
            </div>
        </div>
    </div>

  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue';
import { flexService } from '../../services/flex.js';
import { chatManager } from '../../flex/chatroom/chat-manager.js';

import ChatSidebar from '../chat/ChatSidebar.vue';
import ChatWindow from '../chat/ChatWindow.vue';
import UserList from '../chat/UserList.vue';

// Init Manager
onMounted(() => {
    // Init will now auto-load history and host if needed
    chatManager.init(flexService.node);
});

// State
const manager = chatManager;
const activeSession = computed(() => {
    // Reset unread when viewing
    const s = manager.getActiveSession();
    if (s && s.unread > 0) s.unread = 0;
    return s;
});

const showUserList = ref(false);
const showAddModal = ref(false);
const modalMode = ref('join');
const isBusy = ref(false);

// Form
const formDomain = ref('');
const formName = ref(`${flexService.state.domain}'s chat room`);

// Actions
const selectSession = (id) => {
    manager.state.activeSessionId = id;
};

const sendMessage = (text) => {
    if (activeSession.value) {
        manager.sendMessage(activeSession.value.id, text);
    }
};

const leaveSession = () => {
    if (activeSession.value) {
        manager.leaveSession(activeSession.value.id);
    }
};

const confirmAdd = async () => {
    isBusy.value = true;
    
    // Always use domain as nickname
    const nick = flexService.state.domain || 'User';

    if (modalMode.value === 'host') {
        const success = manager.createHostSession(formName.value, nick);
        if (success) showAddModal.value = false;
        else alert("Failed to start server (Port in use?)");
    } else {
        const success = await manager.joinSession(null, formDomain.value, nick);
        if (success) showAddModal.value = false;
        else alert("Failed to connect");
    }

    isBusy.value = false;
};

</script>

<style scoped>
.chat-tool-v2 {
    display: flex;
    height: 100%;
    width: 100%;
    background: var(--bg-color);
    overflow: hidden;
}

.chat-main {
    flex: 1;
    display: flex;
    position: relative;
    /* transition? */
}

.empty-state {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg-color);
}
.empty-content {
    text-align: center;
    color: var(--text-muted);
}
.empty-content h3 {
    margin-bottom: 20px;
    font-weight: 400;
}

/* Modal */
.modal-overlay {
    position: fixed;
    top: 0; left: 0; right: 0; bottom: 0;
    background: rgba(0,0,0,0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
}

.modal {
    background: var(--panel-bg);
    padding: 24px;
    border-radius: 12px;
    width: 320px;
    box-shadow: 0 4px 20px rgba(0,0,0,0.3);
    border: 1px solid var(--border-color);
}

.modal h3 {
    margin-top: 0;
    margin-bottom: 20px;
}

.tabs {
    display: flex;
    background: var(--bg-darker);
    padding: 4px;
    border-radius: 8px;
    margin-bottom: 20px;
}
.tabs button {
    flex: 1;
    background: transparent;
    border: none;
    color: var(--text-muted);
    padding: 8px;
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
}
.tabs button.active {
    background: var(--bg-light);
    color: var(--text-primary);
    box-shadow: 0 1px 3px rgba(0,0,0,0.1);
}

.modal-body {
    display: flex;
    flex-direction: column;
    gap: 16px;
    margin-bottom: 24px;
}

.form-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
}
.form-group label {
    font-size: 0.85em;
    color: var(--text-secondary);
}
.form-group input {
    background: var(--input-bg);
    border: 1px solid var(--border-color);
    color: var(--text-primary);
    padding: 8px 12px;
    border-radius: 6px;
}

.modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
}

.btn-primary {
    background: var(--accent-color);
    color: white;
    border: none;
    padding: 8px 16px;
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
}
.btn-primary:active {
    transform: translateY(1px);
}
.btn-primary:disabled {
    opacity: 0.6;
}

.btn-cancel {
    background: transparent;
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    padding: 8px 16px;
    border-radius: 6px;
    cursor: pointer;
}
</style>
