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
        <div class="connection-badge" @click="showConnectionModal = true" :class="connectionState">
             <div class="indicator"></div>
             <div class="status-content">
                <span class="status-text">{{ connectionState }}</span>
                <span v-if="activeDomain && connectionState === 'ready'" class="domain-info">{{ activeDomain }}</span>
             </div>
             <Wifi :size="14" v-if="connectionState === 'ready'" />
             <WifiOff :size="14" v-else />
        </div>
        
        <div class="theme-toggle">
            <button @click="toggleTheme" title="Toggle Theme" class="icon-btn">
                <Sun v-if="currentTheme === 'light'" :size="20" />
                <Moon v-else :size="20" />
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
import { ref, computed, watch, onMounted } from 'vue';
import { flexService } from './services/flex.js';
import { 
    Network, Activity, ScrollText, Settings, 
    Wifi, WifiOff, Sun, Moon 
} from 'lucide-vue-next';

import ConnectionModal from './components/ConnectionModal.vue';
import PingTool from './components/tools/PingTool.vue';
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
        case 'logs': return LogPanel;
        case 'settings': return { template: '<div style="padding:40px; text-align:center; color: var(--text-muted);"><h3>Settings</h3><p>Coming Soon...</p></div>' }; 
        default: return PingTool;
    }
});

// Theme
const toggleTheme = () => flexService.toggleTheme();

// Auto-show modal on disconnect
onMounted(() => {
    if (connectionState.value === 'disconnected') {
        showConnectionModal.value = true;
    }
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

.sidebar-footer {
    padding: 16px;
    border-top: 1px solid var(--border-color);
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
}

.connection-badge {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 12px;
    background: var(--bg-color);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-md);
    cursor: pointer;
    user-select: none;
    transition: all 0.2s;
}

.connection-badge:hover {
    border-color: var(--accent-color);
    box-shadow: var(--shadow-sm);
}

.status-content {
    display: flex;
    flex-direction: column;
    font-size: 0.8em;
}

.status-text {
    text-transform: capitalize;
    font-weight: 600;
    line-height: 1.2;
}

.domain-info {
    color: var(--text-muted);
    font-size: 0.9em;
}

.connection-badge.ready {
    border-color: var(--success-color);
    background: rgba(34, 197, 94, 0.05);
}
.connection-badge.ready .status-text { color: var(--success-color); }

.connection-badge.disconnected { border-color: var(--border-color); }
.connection-badge.disconnected .status-text { color: var(--text-muted); }

.connection-badge.connecting { border-color: var(--accent-color); }
.connection-badge.connecting .status-text { color: var(--accent-color); }


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
