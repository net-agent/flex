<template>
  <div class="chat-window">
    <div class="chat-header">
        <div class="header-info">
            <div class="title-row">
                <h2>{{ session.name }}</h2>
                <span v-if="session.type === 'host'" class="host-label">你是聊天室房主</span>
            </div>
            <span class="status">{{ session.client ? 'Connected' : 'Disconnected' }}</span>
        </div>
        <div class="header-actions">
            <button class="btn-text" @click="$emit('toggleUsers')">
                Users ({{ session.users.length }})
            </button>
            <button class="btn-text danger" @click="$emit('leave')">
                <LogOut :size="18" />
            </button>
        </div>
    </div>
    
    <div class="messages" ref="msgContainer">
        <div v-for="(msg, i) in session.messages" :key="i" class="msg-wrapper" :class="{ mine: msg.isMine, sys: msg.type === 'sys', consecutive: checkConsecutive(i) }">
            
            <!-- System Message -->
            <div v-if="msg.type === 'sys'" class="sys-msg">{{ msg.content }}</div>
            
            <!-- Standard Message -->
            <template v-else>
                <!-- Header (Sender) -->
                <div class="msg-meta" v-if="!msg.isMine && !checkConsecutive(i)">
                    <span class="sender">{{ msg.sender }}</span>
                </div>
                
                <!-- Bubble Row -->
                <div class="msg-row">
                    <div class="chat-bubble">
                        <div class="content">
                            {{ msg.content }}
                        </div>
                    </div>
                    <!-- Host Badge: Outside, only first in group -->
                    <span v-if="isHostMessage(msg) && !checkConsecutive(i)" class="host-badge">HOST</span>
                    
                    <span class="time" v-if="!checkConsecutive(i)">{{ formatTime(msg.timestamp) }}</span>
                </div>
            </template>

        </div>
    </div>

    <!-- Input Area -->
    <div class="input-area">
        <textarea 
            v-model="inputMsg" 
            @keydown.enter.prevent="send" 
            placeholder="Type a message..."
        ></textarea>
        <button class="btn-send" @click="send">
            <Send :size="20" />
        </button>
    </div>
  </div>
</template>

<script setup>
import { ref, watch, nextTick } from 'vue';
import { LogOut, Send } from 'lucide-vue-next';

const props = defineProps({
    session: Object
});

const emit = defineEmits(['send', 'leave', 'toggleUsers']);

const inputMsg = ref('');
const msgContainer = ref(null);

const send = () => {
    if (!inputMsg.value.trim()) return;
    emit('send', inputMsg.value);
    inputMsg.value = '';
};

const scrollToBottom = () => {
    nextTick(() => {
        if (msgContainer.value) {
            msgContainer.value.scrollTop = msgContainer.value.scrollHeight;
        }
    });
};

watch(() => props.session.messages, scrollToBottom, { deep: true });
watch(() => props.session.id, scrollToBottom);

const formatTime = (ts) => {
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
};

// Grouping Logic
const checkConsecutive = (index) => {
    if (index === 0) return false;
    const curr = props.session.messages[index];
    const prev = props.session.messages[index - 1];
    
    if (curr.type === 'sys' || prev.type === 'sys') return false;
    if (curr.sender !== prev.sender) return false;
    const diff = curr.timestamp - prev.timestamp;
    if (diff > 2 * 60 * 1000) return false;
    
    return true;
};

// Host Check
const isHostMessage = (msg) => {
    // 1. Server-tagged (for everyone)
    if (msg.isHost) return true;
    // 2. Local fallback (for self when host, though server should tag loopback too)
    return props.session.type === 'host' && msg.isMine;
};
</script>

<style scoped>
.chat-window {
    flex: 1;
    display: flex;
    flex-direction: column;
    background: var(--bg-color);
}

.chat-header {
    padding: 16px 24px;
    border-bottom: 1px solid var(--border-color);
    display: flex;
    justify-content: space-between;
    align-items: center;
    background: var(--bg-color);
}

.header-info {
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.title-row {
    display: flex;
    align-items: center;
    gap: 8px;
}

.header-info h2 {
    margin: 0;
    font-size: 1.2em;
}

.host-label {
    font-size: 0.75em;
    background: var(--accent-light);
    color: var(--accent-color);
    padding: 2px 8px;
    border-radius: 4px;
    font-weight: 600;
}

.status {
    font-size: 0.8em;
    color: var(--success-color);
}

.header-actions {
    display: flex;
    gap: 12px;
}

.btn-text {
    background: none;
    border: none;
    color: var(--text-secondary);
    cursor: pointer;
    padding: 6px 10px;
    border-radius: 6px;
    display: flex;
    align-items: center;
}
.btn-text:hover {
    background: var(--bg-hover);
    color: var(--text-primary);
}
.btn-text.danger:hover {
    color: var(--error-color);
}

.messages {
    flex: 1;
    overflow-y: auto;
    padding: 24px;
    display: flex;
    flex-direction: column;
}

.msg-wrapper {
    display: flex;
    flex-direction: column;
    gap: 4px;
    max-width: 80%;
    margin-top: 16px; 
}
.msg-wrapper.consecutive {
    margin-top: 2px; 
}
.msg-wrapper.mine {
    align-self: flex-end;
    align-items: flex-end;
}
.msg-wrapper.sys {
    align-self: center;
    max-width: 100%;
}

.sys-msg {
    font-size: 0.8em;
    color: var(--text-muted);
    background: var(--bg-darker);
    padding: 4px 12px;
    border-radius: 12px;
}

.msg-meta {
    padding-left: 4px; 
}
.sender {
    font-size: 0.75em;
    color: var(--text-muted);
    font-weight: 600;
}

.msg-row {
    display: flex;
    align-items: flex-end;
    gap: 8px;
}
.msg-wrapper.mine .msg-row {
    flex-direction: row-reverse;
}

.chat-bubble {
    background: var(--panel-bg); 
    border: 1px solid var(--border-color); 
    color: var(--text-primary);
    padding: 0.5em 1em;
    border-radius: 12px;
    border-top-left-radius: 2px;
    position: relative;
    box-shadow: 0 1px 2px rgba(0,0,0,0.05);
}

.msg-wrapper.mine .chat-bubble {
    background: var(--accent-color);
    color: white;
    border: none; 
    border-radius: 12px;
    border-top-right-radius: 2px;
}

/* Host Badge Style */
.host-badge {
    font-size: 0.6em;
    padding: 2px 4px;
    border-radius: 4px;
    font-weight: 700;
    line-height: 1;
    margin-bottom: 2px; /* Align with bottom */
    
    background: var(--accent-light);
    color: var(--accent-color);
    border: 1px solid var(--accent-color);
    opacity: 0.8;
}

.content {
    line-height: 1.5;
    word-break: break-word;
}
.msg-wrapper.mine .content {
    color: white;
}

.time {
    font-size: 0.7em;
    color: var(--text-muted);
    font-family: inherit;
    margin-bottom: 2px; 
    white-space: nowrap;
    min-width: 50px; 
}
.msg-wrapper.mine .time {
    text-align: right;
}

.input-area {
    padding: 20px;
    border-top: 1px solid var(--border-color);
    display: flex;
    gap: 12px;
    background: var(--bg-color);
}

textarea {
    flex: 1;
    background: var(--input-bg);
    border: 1px solid var(--border-color);
    color: var(--text-primary);
    padding: 12px;
    border-radius: 8px;
    resize: none;
    height: 50px;
    outline: none;
    font-family: inherit;
}
textarea:focus {
    border-color: var(--accent-color);
}

.btn-send {
    width: 50px;
    height: 50px;
    background: var(--accent-color);
    color: white;
    border: none;
    border-radius: 8px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
}
.btn-send:hover {
    filter: brightness(1.1);
}
</style>
