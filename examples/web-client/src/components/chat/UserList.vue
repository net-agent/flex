<template>
  <div class="user-list">
    <div class="list-header">
        <h3>Members</h3>
        <button @click="$emit('close')" class="close-btn">×</button>
    </div>
    <div class="list-content">
        <div v-for="u in users" :key="u" class="user-item">
            <div class="u-avatar">{{ u.substring(0,1).toUpperCase() }}</div>
            <span class="u-name">{{ u }}</span>
        </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
    users: Array
});
defineEmits(['close']);
</script>

<style scoped>
.user-list {
    width: 200px;
    border-left: 1px solid var(--border-color);
    background: var(--bg-darker);
    display: flex;
    flex-direction: column;
}

.list-header {
    padding: 16px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-bottom: 1px solid var(--border-color);
}

.list-header h3 {
    margin: 0;
    font-size: 1em;
}

.close-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 1.5em;
    cursor: pointer;
}

.list-content {
    flex: 1;
    overflow-y: auto;
    padding: 10px;
}

.user-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px;
    border-radius: 6px;
}
.user-item:hover {
    background: var(--bg-hover);
}

.u-avatar {
    width: 32px;
    height: 32px;
    background: var(--bg-light);
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.9em;
    font-weight: 600;
}

.u-name {
    font-size: 0.9em;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}
</style>
