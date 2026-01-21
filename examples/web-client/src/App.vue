<template>
  <div class="app-layout" :data-theme="currentTheme">
    <!-- Middle Area (Sidebar + Main) -->
    <div class="middle-area">
        <!-- Sidebar -->
        <div class="sidebar">
          <div class="brand">
            <Network :size="28" class="brand-icon" />
          </div>
          
          <nav class="nav-menu">
            <div class="nav-group">
                <a 
                  href="#" 
                  :class="{ active: currentView === 'chat' }" 
                  @click.prevent="currentView = 'chat'"
                  title="Chat"
                >
                  <MessageSquare :size="22" />
                  <span class="nav-label">Chat</span>
                </a>
            </div>

            <div class="nav-group">
                <a 
                  href="#" 
                  :class="{ active: currentView === 'ping' }" 
                  @click.prevent="currentView = 'ping'"
                  title="Ping Tool"
                >
                  <Activity :size="22" />
                  <span class="nav-label">Ping</span>
                </a>
                <a 
                  href="#" 
                  :class="{ active: currentView === 'stream' }" 
                  @click.prevent="currentView = 'stream'"
                  title="Stream Tool"
                >
                  <Radio :size="22" />
                  <span class="nav-label">Stream</span>
                </a>
            </div>

            <div class="nav-group">
                <a 
                  href="#" 
                  :class="{ active: currentView === 'packets' }" 
                  @click.prevent="currentView = 'packets'"
                  title="Raw Packets"
                >
                  <Binary :size="22" />
                  <span class="nav-label">Packets</span>
                </a>
                <a 
                  href="#" 
                  :class="{ active: currentView === 'logs' }" 
                  @click.prevent="currentView = 'logs'"
                  title="System Logs"
                >
                  <ScrollText :size="22" />
                  <span class="nav-label">Logs</span>
                </a>
                <a 
                  href="#" 
                  :class="{ active: currentView === 'settings' }" 
                  @click.prevent="currentView = 'settings'"
                  title="Settings"
                >
                  <Settings :size="22" />
                  <span class="nav-label">Settings</span>
                </a>
            </div>
          </nav>
        </div>
        
        <!-- Main Content -->
        <div class="main-content">
          <Transition name="fade" mode="out-in">
            <keep-alive>
                <component :is="activeComponent" />
            </keep-alive>
          </Transition>
        </div>
    </div>

    <!-- Global Status Bar (VS Code Style) -->
    <div class="global-status-bar">
        <div class="gsb-left" @click="showConnectionModal = true">
            <div class="gsb-item clickable connection-pill" :class="connectionState">
                <div class="status-icon">
                     <Wifi v-if="connectionState === 'ready'" :size="14" />
                     <WifiOff v-else :size="14" />
                </div>
                <!-- Shorten text for cleaner bar -->
                <span>{{ connectionState === 'ready' ? activeDomain : 'Disconnected' }}</span>
            </div>
            
             <div class="gsb-item" v-if="connectionState === 'ready'">
                <Clock :size="14" />
                <span>{{ uptime }}</span>
            </div>
        </div>

        <div class="gsb-right">
             <div class="gsb-item clickable" @click="toggleTheme" title="Toggle Theme">
                <Sun v-if="currentTheme === 'light'" :size="14" />
                <Moon v-else :size="14" />
            </div>
            <div class="gsb-item">
                <span style="font-size:0.9em">FlexClient 0.1</span>
            </div>
        </div>
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
    Wifi, WifiOff, Sun, Moon, MessageSquare, Binary,
    Clock 
} from 'lucide-vue-next';

import ConnectionModal from './components/ConnectionModal.vue';
import PingTool from './components/tools/PingTool.vue';
import StreamTool from './components/tools/StreamTool.vue';
import ChatTool from './components/tools/ChatTool.vue';
import PacketTool from './components/tools/PacketTool.vue';
import LogPanel from './components/LogPanel.vue';

// State
const currentView = ref(localStorage.getItem('flex_last_view') || 'ping');
const showConnectionModal = ref(false);

watch(currentView, (newVal) => {
    localStorage.setItem('flex_last_view', newVal);
});

const currentTheme = computed(() => flexService.state.theme);
const connectionState = computed(() => flexService.state.connectionState);
const activeDomain = computed(() => flexService.state.domain);

// Component Mapping
const activeComponent = computed(() => {
    switch (currentView.value) {
        case 'ping': return PingTool;
        case 'stream': return StreamTool;
        case 'chat': return ChatTool;
        case 'packets': return PacketTool;
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
  flex-direction: column; /* CHANGE: Column layout for Header/Middle/Footer */
  height: 100vh;
  width: 100vw;
  background-color: var(--bg-color);
  color: var(--text-primary);
}

.middle-area {
    flex: 1;
    display: flex; /* Row layout for Sidebar + content */
    overflow: hidden;
}

.sidebar {
  width: 72px; 
  background-color: var(--sidebar-bg);
  border-right: 1px solid var(--border-color);
  display: flex;
  flex-direction: column;
  transition: width 0.3s;
  z-index: 10;
}

.brand {
    height: 60px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--accent-hover);
    border-bottom: 1px solid transparent; 
}

.nav-menu {
    flex: 1;
    padding: 16px 0;
    display: flex;
    flex-direction: column;
    gap: 16px;
    align-items: center;
}

.nav-group {
    display: flex;
    flex-direction: column;
    gap: 8px;
    align-items: center;
    width: 100%;
}

.nav-group label {
    display: none; 
}

.nav-menu a {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    width: 56px;
    height: 56px;
    gap: 4px;
    color: var(--text-muted);
    text-decoration: none;
    border-radius: 12px;
    transition: all 0.2s;
}

.nav-label {
    font-size: 10px;
    font-weight: 500;
    line-height: 1;
}

.nav-menu a:hover {
    background: var(--bg-hover);
    color: var(--text-primary);
}

.nav-menu a.active {
    background: var(--accent-color);
    color: white;
    box-shadow: 0 4px 12px rgba(var(--accent-rgb), 0.3);
}

.main-content {
  flex: 1;
  overflow: hidden;
  position: relative;
  background: var(--bg-color);
}

/* Global Status Bar */
.global-status-bar {
    height: 24px;
    background: var(--sidebar-bg); /* Neutral background */
    border-top: 1px solid var(--border-color);
    color: var(--text-secondary);
    display: flex;
    justify-content: space-between; /* Left and Right sections */
    align-items: center;
    padding: 0 8px; /* Reduced padding since pill has padding */
    font-size: 11px;
    font-family: 'JetBrains Mono', monospace; /* Tech feel */
    user-select: none;
}

.gsb-left, .gsb-right {
    display: flex;
    align-items: center;
    gap: 8px; /* Reduced gap */
    height: 100%;
}

.gsb-item {
    display: flex;
    align-items: center;
    gap: 6px;
    height: 80%; /* Slightly smaller than bar */
    padding: 0 8px;
    border-radius: 4px; /* Soft edges */
    cursor: default;
    transition: all 0.2s;
}

/* Hover effects for standard items */
.gsb-item:not(.connection-pill).clickable:hover {
    background: var(--bg-hover);
    color: var(--text-primary);
    cursor: pointer;
}

/* Connection Pill Styling */
.connection-pill {
    color: white;
    font-weight: 500;
}
.connection-pill.ready {
    background: var(--success-color);
}
.connection-pill.disconnected {
    background: var(--error-color);
}
.connection-pill.connecting {
    background: var(--accent-color);
    animation: pulse 1s infinite;
}
/* Hover effect for pill */
.connection-pill:hover {
    opacity: 0.9;
    cursor: pointer;
}


.status-icon {
    display: flex;
    align-items: center;
}

/* Animations */
@keyframes pulse {
    0% { opacity: 0.8; }
    50% { opacity: 1; }
    100% { opacity: 0.8; }
}
</style>
