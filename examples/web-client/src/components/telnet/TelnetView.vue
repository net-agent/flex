<template>
    <ToolLayout>
        <template #icon>
            <Terminal :size="20" />
        </template>
        <template #title>Telnet Tool</template>
        
        <div class="stream-container">
            <div class="split-layout">
                <TelnetClientPanel />
                <TelnetServerPanel />
            </div>
        </div>
    </ToolLayout>
</template>

<script>
export default {
  name: 'TelnetView'
}
</script>

<script setup>
import ToolLayout from '../common/ToolLayout.vue';
import TelnetClientPanel from './TelnetClientPanel.vue';
import TelnetServerPanel from './TelnetServerPanel.vue';
import { Terminal } from 'lucide-vue-next';
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
