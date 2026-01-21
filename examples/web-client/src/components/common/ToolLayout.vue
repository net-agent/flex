<template>
  <div class="tool-layout">
    <div class="tool-header">
        <div class="title-section">
            <div class="icon-wrapper">
                <slot name="icon"></slot>
            </div>
            <h3>
                <slot name="title">Tool Name</slot>
            </h3>
        </div>
        <div class="actions-section">
            <slot name="actions"></slot>
        </div>
    </div>
    
    <div class="tool-body">
        <slot></slot>
    </div>
  </div>
</template>

<style scoped>
.tool-layout {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--bg-color);
}

.tool-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 24px;
    background: var(--bg-color);
    border-bottom: 1px solid var(--border-color);
    flex-shrink: 0;
}

.title-section {
    display: flex;
    align-items: center;
    gap: 12px;
}

.icon-wrapper {
    color: var(--accent-color);
    display: flex;
    align-items: center;
}

h3 {
    margin: 0;
    font-size: 1.1em;
    font-weight: 600;
    color: var(--text-primary);
}

.actions-section {
    display: flex;
    gap: 8px;
}

.tool-body {
    flex: 1;
    overflow: hidden; /* Let children handle scrolling if needed */
    position: relative;
    display: flex;
    flex-direction: column;
}
</style>
