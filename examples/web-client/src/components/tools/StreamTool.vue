<template>
    <ToolLayout>
        <template #icon>
            <Radio :size="20" />
        </template>
        <template #title>Stream Verification</template>
        
        <div class="stream-container">
            <div class="split-layout">
                <StreamClientPanel />
                <StreamServerPanel />
            </div>
        </div>
    </ToolLayout>
</template>

<script>
export default {
  name: 'StreamTool'
}
</script>

<script setup>
import ToolLayout from '../common/ToolLayout.vue';
import StreamClientPanel from './StreamClientPanel.vue';
import StreamServerPanel from './StreamServerPanel.vue';
import { Radio } from 'lucide-vue-next';
</script>

<style scoped>
.stream-container {
    height: 100%;
    padding: 24px;
    overflow: hidden; 
}

.split-layout {
    display: flex;
    gap: 20px;
    height: 100%;
    max-width: 1200px;
    margin: 0 auto;
}
</style>
