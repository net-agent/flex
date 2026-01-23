<template>
  <ToolLayout>
    <template #icon>
        <ScrollText :size="20" />
    </template>
    <template #title>System Logs</template>
    <template #actions>
        <button @click="clearLogs" class="action-btn" title="Clear Logs">
            <Trash2 :size="16" />
        </button>
    </template>

    <div class="log-container" ref="containerRef">
      <div v-for="(log, index) in logs" :key="index" class="log-entry">
        {{ log }}
      </div>
      <div v-if="logs.length === 0" class="empty-hint">
        <Ghost :size="24" />
        <span>No logs recorded yet...</span>
      </div>
    </div>
  </ToolLayout>
</template>

<script setup>
import { computed, ref, watch, nextTick } from 'vue';
import { flexService } from '../../../services/flex.js';
import { ScrollText, Trash2, Ghost } from 'lucide-vue-next';
import ToolLayout from '../../common/ToolLayout.vue';

const logs = computed(() => flexService.state.logs);
const containerRef = ref(null);

const clearLogs = () => {
    flexService.state.logs.length = 0; 
};

// Auto-scroll to bottom when logs change
watch(logs, async () => {
    await nextTick();
    if (containerRef.value) {
        containerRef.value.scrollTop = containerRef.value.scrollHeight;
    }
}, { deep: true });
</script>

<style scoped>
.action-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    border: 1px solid transparent;
    background: transparent;
    border-radius: var(--radius-sm);
    color: var(--text-secondary);
    cursor: pointer;
}
.action-btn:hover {
    background: var(--bg-color);
    border-color: var(--border-color);
    color: var(--danger-color);
}

.log-container {
    flex: 1;
    overflow-y: auto;
    padding: 12px;
    font-family: 'JetBrains Mono', 'Consolas', monospace;
    font-size: 0.85em;
    color: var(--log-text);
    background: var(--log-bg);
}

.log-entry {
    margin-bottom: 4px;
    word-break: break-all;
    line-height: 1.5;
    padding-left: 8px;
    border-left: 2px solid transparent;
}
.log-entry:hover {
    background: rgba(0,0,0,0.02);
    border-left-color: var(--accent-color);
}

.empty-hint {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--text-muted);
    opacity: 0.5;
    margin-top: 40px;
    height: 100%;
}
</style>
