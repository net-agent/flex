
export class PortManager {
    constructor(min = 100, max = 65535) {
        this.min = min;
        this.max = max;
        this.used = new Set();
        this.current = min; // Simple round-robin strategy
    }

    /**
     * Get a free port number.
     * @returns {number} The allocated port number.
     * @throws {Error} If no ports are available.
     */
    getFreePort() {
        const start = this.current;

        do {
            const port = this.current;
            // Move to next for subsequent calls
            this.current++;
            if (this.current > this.max) {
                this.current = this.min;
            }

            if (!this.used.has(port)) {
                this.used.add(port);
                return port;
            }
        } while (this.current !== start);

        throw new Error("no free ports available");
    }

    /**
     * Mark a specific port as used.
     * @param {number} port 
     */
    usePort(port) {
        if (port < this.min || port > this.max) {
            throw new Error(`invalid port number: ${port}`);
        }
        if (this.used.has(port)) {
            throw new Error(`port ${port} is already used`);
        }
        this.used.add(port);
    }

    /**
     * Release a port.
     * @param {number} port 
     */
    releasePort(port) {
        this.used.delete(port);
    }

    /**
     * Check if port is used.
     * @param {number} port 
     * @returns {boolean}
     */
    isUsed(port) {
        return this.used.has(port);
    }
}
