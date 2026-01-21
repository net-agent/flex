
export class PortManager {
    constructor(min = 1, max = 65535) {
        this.min = min;
        this.max = max;

        // IANA Dynamic Ports range (49152 to 65535)
        this.ephemeralMin = 49152;
        this.ephemeralMax = 65535;

        this.used = new Set();
        this.current = this.ephemeralMin; // Start allocation from Ephemeral range
    }

    /**
     * Get a free port number from the ephemeral range.
     * @returns {number} The allocated port number.
     * @throws {Error} If no ports are available.
     */
    getFreePort() {
        const start = this.current;

        do {
            const port = this.current;
            // Move to next for subsequent calls
            this.current++;
            if (this.current > this.ephemeralMax) {
                this.current = this.ephemeralMin;
            }

            if (!this.used.has(port)) {
                this.used.add(port);
                return port;
            }
        } while (this.current !== start);

        throw new Error("no free ephemeral ports available");
    }

    /**
     * Mark a specific port as used.
     * Allows binding to ANY valid port (1-65535) if free.
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
