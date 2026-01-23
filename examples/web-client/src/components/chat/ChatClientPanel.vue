<template>
  <div class="chat-client-panel">
      <!-- Re-using existing components broadly but wired to new manager -->
      <!-- Sidebar -->
      <ChatSidebar 
          :sessions="clientManager.state.sessions" 
          :activeId="clientManager.state.activeSessionId" 
          @select="selectSession"
          @add="showAddModal = true"
      />

      <!-- Main Chat Area -->
      <div class="client-main" v-if="activeSession">
         <ChatWindow 
             :session="activeSession"
             @send="sendMessage"
             @leave="leaveSession"
             @toggleUsers="showUserList = !showUserList"
         />
         
         <UserList 
             v-if="showUserList" 
             :users="activeSession.users" 
             @close="showUserList = false" 
         />
      </div>

      <div class="empty-state" v-else>
          <div class="empty-content">
              <h3>No Chat Selected</h3>
              <button class="btn-primary" @click="showAddModal = true">Join a Room</button>
          </div>
      </div>

      <!-- Add (Join) Room Modal -->
      <div v-if="showAddModal" class="modal-overlay" @click.self="showAddModal = false">
          <div class="modal">
              <h3>Join Chat Room</h3>
              <div class="modal-body">
                  <div class="form-group">
                      <label>Target Domain (e.g., web-xxxxx)</label>
                      <input 
                        v-model="formDomain" 
                        type="text" 
                        placeholder="Domain to join" 
                        @keyup.enter="confirmJoin"
                      />
                  </div>
              </div>
              <div class="modal-actions">
                  <button class="btn-cancel" @click="showAddModal = false">Cancel</button>
                  <button class="btn-primary" @click="confirmJoin" :disabled="isBusy">
                      {{ isBusy ? 'Joining...' : 'Join' }}
                  </button>
              </div>
          </div>
      </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue';
import { chatClientManager } from '../../flex/chatroom/client-manager.js';
import { flexService } from '../../services/flex.js';

import ChatSidebar from './ChatSidebar.vue';
import ChatWindow from './ChatWindow.vue';
import UserList from './UserList.vue';

const clientManager = chatClientManager;

const isBusy = ref(false);
const showAddModal = ref(false);
const showUserList = ref(false);
const formDomain = ref('');

const activeSession = computed(() => {
    const id = clientManager.state.activeSessionId;
    const s = clientManager.state.sessions.find(x => x.id === id);
    if (s && s.unread > 0) s.unread = 0;
    return s;
});

const selectSession = (id) => {
    clientManager.state.activeSessionId = id;
};

const sendMessage = (text) => {
    if (activeSession.value) {
        clientManager.sendMessage(activeSession.value.id, text);
    }
};

const leaveSession = () => {
    if (activeSession.value) {
        clientManager.leaveSession(activeSession.value.id);
    }
};

const confirmJoin = async () => {
    if (!formDomain.value) return;
    isBusy.value = true;
    const nick = flexService.state.domain || 'User';
    
    const success = await clientManager.joinSession(null, formDomain.value, nick);
    if (success) {
        showAddModal.value = false;
        formDomain.value = '';
    } else {
        alert("Failed to connect to room");
    }
    isBusy.value = false;
};
</script>

<style scoped>
.chat-client-panel {
    display: flex;
    height: 100%;
    width: 100%;
    background: var(--bg-color);
    overflow: hidden;
}

.client-main {
    flex: 1;
    display: flex;
    position: relative;
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

.btn-primary {
    background: var(--accent-color);
    color: white;
    border: none;
    padding: 8px 16px;
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
}
.btn-cancel {
    background: transparent;
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    padding: 8px 16px;
    border-radius: 6px;
    cursor: pointer;
}

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
.modal-body {
    display: flex;
    flex-direction: column;
    gap: 16px;
    margin-bottom: 24px;
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
.modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
}
</style>
