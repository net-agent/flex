export class EventEmitter {
    constructor() {
        this.handlers = {};
    }

    on(event, handler) {
        if (!this.handlers[event]) {
            this.handlers[event] = [];
        }
        this.handlers[event].push(handler);
        return this;
    }

    off(event, handler) {
        if (!this.handlers[event]) return this;
        this.handlers[event] = this.handlers[event].filter(h => h !== handler);
        return this;
    }

    once(event, handler) {
        const wrapper = (...args) => {
            this.off(event, wrapper);
            handler(...args);
        };
        this.on(event, wrapper);
        return this;
    }

    emit(event, ...args) {
        if (this.handlers[event]) {
            this.handlers[event].forEach(h => {
                try {
                    h(...args);
                } catch (e) {
                    console.error(`[EventEmitter] Error in handler for '${event}':`, e);
                }
            });
        }
        return this;
    }
}
