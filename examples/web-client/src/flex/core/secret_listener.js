import { SecretStream } from './secret_stream.js';
import { Stream } from './stream.js';

/**
 * Listens on a port for encrypted connections.
 * Matches Go's NewSecretListener.
 * 
 * @param {FlexNode} node - The FlexNode instance
 * @param {number} port - Port to listen on
 * @param {string} secret - Shared secret
 * @param {function(SecretStream)} callback - Called with successful encrypted stream
 * @returns {boolean} - Success
 */
export function ListenSecret(node, port, secret, callback) {

    // We modify the callback to wrap the stream
    const wrappedCallback = (rawStream) => {
        // Create SecretStream wrapper
        const secretStream = new SecretStream(rawStream, secret);

        // Handle Handshake
        secretStream.init().then(() => {
            // Success
            callback(secretStream);
        }).catch(err => {
            console.error(`[SecretListener] Handshake failed from ${rawStream.remoteIP}:${rawStream.remotePort} - ${err.message}`);
            // Close raw stream on failure
            rawStream.close();
        });
    };

    return node.listen(port, wrappedCallback);
}
