<template>
  <div class="app-layout" :data-theme="currentTheme">
    <!-- Sidebar -->
    <div class="sidebar">
      <div class="brand">
        <Network :size="24" class="brand-icon" />
        <h1>Flex<span>Client</span></h1>
      </div>
      
      <nav class="nav-menu">
        <a 
          href="#" 
          :class="{ active: currentView === 'ping' }" 
          @click.prevent="currentView = 'ping'"
        >
          <Activity :size="18" />
          <span>Ping Tool</span>
        </a>
        <a 
          href="#" 
          :class="{ active: currentView === 'stream' }" 
          @click.prevent="currentView = 'stream'"
        >
          <Radio :size="18" />
          <span>Stream Tool</span>
        </a>
        <a 
          href="#" 
          :class="{ active: currentView === 'logs' }" 
          @click.prevent="currentView = 'logs'"
        >
          <ScrollText :size="18" />
          <span>Logs</span>
        </a>
        <a 
          href="#" 
          :class="{ active: currentView === 'settings' }" 
          @click.prevent="currentView = 'settings'"
        >
          <Settings :size="18" />
          <span>Settings</span>
        </a>
      </nav>

      <div class="sidebar-footer">
        <div class="status-bar-minimal" @click="showConnectionModal = true">
            <div class="status-left">
                <div class="status-indicator" :class="connectionState"></div>
                <div class="status-info">
                    <span class="domain-text">{{ connectionState === 'ready' ? activeDomain : 'Offline' }}</span>
                    <span v-if="connectionState === 'ready'" class="uptime-text">{{ uptime }}</span>
                    <span v-else class="uptime-text">{{ connectionState }}</span>
                </div>
            </div>
            
            <button @click.stop="toggleTheme" class="theme-btn-minimal" title="Toggle Theme">
                <Sun v-if="currentTheme === 'light'" :size="18" />
                <Moon v-else :size="18" />
            </button>
        </div>
      </div>
    </div>
    
    <!-- Main Content -->
    <div class="main-content">
      <Transition name="fade" mode="out-in">
        <keep-alive>
            <component :is="activeComponent" />
        </keep-alive>
      </Transition>
    </div>

    <!-- Global Connection Modal -->
    <ConnectionModal v-model="showConnectionModal" />
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue';
import { flexService } from './services/flex.js';
import { 
    Network, Activity, ScrollText, Settings, Radio,
    Wifi, WifiOff, Sun, Moon 
} from 'lucide-vue-next';

import ConnectionModal from './components/ConnectionModal.vue';
import PingTool from './components/tools/PingTool.vue';
import StreamTool from './components/tools/StreamTool.vue';
import LogPanel from './components/LogPanel.vue';

// State
const currentView = ref('ping');
const showConnectionModal = ref(false);

const currentTheme = computed(() => flexService.state.theme);
const connectionState = computed(() => flexService.state.connectionState);
const activeDomain = computed(() => flexService.state.domain);

// Component Mapping
const activeComponent = computed(() => {
    switch (currentView.value) {
        case 'ping': return PingTool;
        case 'stream': return StreamTool;
        case 'logs': return LogPanel;
        case 'settings': return { template: '<div style="padding:40px; text-align:center; color: var(--text-muted);"><h3>Settings</h3><p>Coming Soon...</p></div>' }; 
        default: return PingTool;
    }
});

// Theme
const toggleTheme = () => flexService.toggleTheme();

// Auto-show modal on disconnect
// Uptime Timer
const uptime = ref('');
let timer = null;

const updateTimer = () => {
    if (flexService.state.connectedSince && flexService.state.connectionState === 'ready') {
        const diff = Math.floor((Date.now() - flexService.state.connectedSince) / 1000);
        const h = Math.floor(diff / 3600).toString().padStart(2, '0');
        const m = Math.floor((diff % 3600) / 60).toString().padStart(2, '0');
        const s = (diff % 60).toString().padStart(2, '0');
        uptime.value = `${h}:${m}:${s}`;
    } else {
        uptime.value = '';
    }
};

onMounted(() => {
    timer = setInterval(updateTimer, 1000);
    if (connectionState.value === 'disconnected') {
        showConnectionModal.value = true;
    }
});

onUnmounted(() => {
    if (timer) clearInterval(timer);
});

watch(connectionState, (newState) => {
    if (newState === 'disconnected') {
        showConnectionModal.value = true;
    } else if (newState === 'ready') {
        showConnectionModal.value = false;
    }
});

</script>

<style scoped>
.app-layout {
  display: flex;
  height: 100vh;
  width: 100vw;
  background-color: var(--bg-color);
  color: var(--text-primary);
}

.sidebar {
  width: 260px;
  background-color: var(--sidebar-bg);
  border-right: 1px solid var(--border-color);
  display: flex;
  flex-direction: column;
}

.brand {
    padding: 24px;
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--accent-hover);
}

.brand h1 {
    margin: 0;
    font-size: 1.4em;
    color: var(--text-primary);
    font-weight: 700;
}

.brand span {
    color: var(--accent-color);
    font-weight: 400;
}

.nav-menu {
    flex: 1;
    padding: 20px 10px;
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.nav-menu a {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 16px;
    color: var(--text-secondary);
    text-decoration: none;
    border-radius: var(--radius-md);
    transition: all 0.2s;
    font-weight: 500;
}

.nav-menu a:hover {
    background: var(--bg-color);
    color: var(--text-primary);
}

.nav-menu a.active {
    background: var(--accent-light);
    color: var(--accent-color);
}



/* ... existing styles ... */

.sidebar-footer {
    padding: 0;
    margin-top: auto; /* Push to bottom */
    display: flex;
    flex-direction: column;
}

.status-bar-minimal {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 20px;
    background: var(--status-bar-bg);
    border-top: 1px solid var(--status-bar-border);
    cursor: pointer;
    transition: background 0.2s;
}

.status-bar-minimal:hover {
    background: var(--status-bar-hover);
}

.status-left {
    display: flex;
    align-items: center;
    gap: 12px;
}

.status-indicator {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--text-muted);
    box-shadow: 0 0 0 2px rgba(255, 255, 255, 0.05);
}

.status-indicator.ready {
    background: var(--success-color);
    box-shadow: 0 0 8px var(--success-color);
    animation: pulse 2s infinite;
}

.status-indicator.connecting {
    background: var(--accent-color);
    animation: pulse 1s infinite;
}

.status-info {
    display: flex;
    flex-direction: column;
    justify-content: center;
    line-height: 1.2;
}

.domain-text {
    font-weight: 600;
    font-size: 0.9em;
    color: var(--text-primary);
}

.uptime-text {
    font-size: 0.75em;
    color: var(--text-muted);
    font-family: 'JetBrains Mono', monospace;
}

.theme-btn-minimal {
    background: transparent;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 8px;
    border-radius: var(--radius-md);
    display: flex;
    align-items: center;
    justify-content: center;
    transition: all 0.2s;
}

.theme-btn-minimal:hover {
    color: var(--text-primary);
    background: rgba(255, 255, 255, 0.1);
}

@keyframes pulse {
    0% { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0.4); }
    70% { box-shadow: 0 0 0 6px rgba(34, 197, 94, 0); }
    100% { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0); }
}

/* ... REMOVE OLD BADGE STYLES ... */



.icon-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    background: transparent;
    border: 1px solid transparent;
    border-radius: var(--radius-md);
    color: var(--text-secondary);
}
.icon-btn:hover {
    background: var(--bg-color);
    color: var(--text-primary);
    border-color: var(--border-color);
}

.main-content {
  flex: 1;
  overflow: hidden;
  position: relative;
  background: var(--bg-color);
}
</style>
