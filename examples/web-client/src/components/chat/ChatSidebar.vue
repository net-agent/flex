<template>
  <div class="chat-sidebar">
    <div class="sidebar-header">
      <h3>Chats</h3>
      <button @click="$emit('add')" class="btn-icon" title="New Chat">+</button>
    </div>
    
    <div class="session-list">
        <div 
            v-for="s in sessions" 
            :key="s.id"
            class="session-item"
            :class="{ active: activeId === s.id }"
            @click="$emit('select', s.id)"
        >
            <div class="avatar">{{ s.name.substring(0,2).toUpperCase() }}</div>
            <div class="info">
                <div class="name">{{ s.name }}</div>
                <div class="last-msg" v-if="s.messages.length > 0">
                    {{ s.messages[s.messages.length-1].content }}
                </div>
            </div>
            <div class="badge" v-if="s.unread > 0">{{ s.unread }}</div>
        </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
    sessions: Array,
    activeId: String
});
defineEmits(['select', 'add']);
</script>

<style scoped>
.chat-sidebar {
    width: 250px;
    background: var(--bg-darker);
    border-right: 1px solid var(--border-color);
    display: flex;
    flex-direction: column;
}

.sidebar-header {
    padding: 16px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-bottom: 1px solid var(--border-color);
}

.sidebar-header h3 {
    margin: 0;
    font-size: 1.1em;
}

.btn-icon {
    background: none;
    border: none;
    color: var(--accent-color);
    font-size: 1.5em;
    cursor: pointer;
    line-height: 1;
}

.session-list {
    flex: 1;
    overflow-y: auto;
}

.session-item {
    padding: 12px 16px;
    display: flex;
    align-items: center;
    gap: 12px;
    cursor: pointer;
    transition: background 0.2s;
}

.session-item:hover {
    background: var(--bg-hover);
}

.session-item.active {
    background: var(--active-bg);
    border-left: 3px solid var(--accent-color);
}

.avatar {
    width: 40px;
    height: 40px;
    background: var(--accent-hover);
    border-radius: 50%;
    color: white;
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: bold;
    font-size: 0.9em;
}

.info {
    flex: 1;
    overflow: hidden;
}

.name {
    font-weight: 600;
    margin-bottom: 4px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.last-msg {
    font-size: 0.8em;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.badge {
    background: var(--error-color);
    color: white;
    font-size: 0.75em;
    min-width: 18px;
    height: 18px;
    border-radius: 9px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 4px;
}
</style>
