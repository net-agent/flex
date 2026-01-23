/**
 * MD5 hash implementation
 * @param {Uint8Array} data
 * @returns {Uint8Array} 16-byte hash
 */
export function md5(data) {
    let msg = data;
    // Pre-processing
    // Append "1" bit to message
    // Append "0" bits until message length in bits ≡ 448 (mod 512)
    // Append length (64 bits, little-endian)

    const len = msg.length * 8;
    const n = msg.length;
    
    // items in 32-bit words
    const nWords = Math.ceil((n + 9) / 64) * 16;
    const X = new Uint32Array(nWords);

    let i;
    for (i = 0; i < n; i++) {
        X[i >>> 2] |= msg[i] << ((i % 4) * 8);
    }
    
    X[i >>> 2] |= 0x80 << ((i % 4) * 8);
    
    X[nWords - 2] = len >>> 0;
    X[nWords - 1] = Math.floor(len / 4294967296);

    let a = 0x67452301;
    let b = 0xefcdab89;
    let c = 0x98badcfe;
    let d = 0x10325476;

    const S11 = 7, S12 = 12, S13 = 17, S14 = 22;
    const S21 = 5, S22 = 9, S23 = 14, S24 = 20;
    const S31 = 4, S32 = 11, S33 = 16, S34 = 23;
    const S41 = 6, S42 = 10, S43 = 15, S44 = 21;

    for (let k = 0; k < nWords; k += 16) {
        const AA = a;
        const BB = b;
        const CC = c;
        const DD = d;

        a = FF(a, b, c, d, X[k + 0], S11, 0xd76aa478);
        d = FF(d, a, b, c, X[k + 1], S12, 0xe8c7b756);
        c = FF(c, d, a, b, X[k + 2], S13, 0x242070db);
        b = FF(b, c, d, a, X[k + 3], S14, 0xc1bdceee);
        a = FF(a, b, c, d, X[k + 4], S11, 0xf57c0faf);
        d = FF(d, a, b, c, X[k + 5], S12, 0x4787c62a);
        c = FF(c, d, a, b, X[k + 6], S13, 0xa8304613);
        b = FF(b, c, d, a, X[k + 7], S14, 0xfd469501);
        a = FF(a, b, c, d, X[k + 8], S11, 0x698098d8);
        d = FF(d, a, b, c, X[k + 9], S12, 0x8b44f7af);
        c = FF(c, d, a, b, X[k + 10], S13, 0xffff5bb1);
        b = FF(b, c, d, a, X[k + 11], S14, 0x895cd7be);
        a = FF(a, b, c, d, X[k + 12], S11, 0x6b901122);
        d = FF(d, a, b, c, X[k + 13], S12, 0xfd987193);
        c = FF(c, d, a, b, X[k + 14], S13, 0xa679438e);
        b = FF(b, c, d, a, X[k + 15], S14, 0x49b40821);

        a = GG(a, b, c, d, X[k + 1], S21, 0xf61e2562);
        d = GG(d, a, b, c, X[k + 6], S22, 0xc040b340);
        c = GG(c, d, a, b, X[k + 11], S23, 0x265e5a51);
        b = GG(b, c, d, a, X[k + 0], S24, 0xe9b6c7aa);
        a = GG(a, b, c, d, X[k + 5], S21, 0xd62f105d);
        d = GG(d, a, b, c, X[k + 10], S22, 0x02441453);
        c = GG(c, d, a, b, X[k + 15], S23, 0xd8a1e681);
        b = GG(b, c, d, a, X[k + 4], S24, 0xe7d3fbc8);
        a = GG(a, b, c, d, X[k + 9], S21, 0x21e1cde6);
        d = GG(d, a, b, c, X[k + 14], S22, 0xc33707d6);
        c = GG(c, d, a, b, X[k + 3], S23, 0xf4d50d87);
        b = GG(b, c, d, a, X[k + 8], S24, 0x455a14ed);
        a = GG(a, b, c, d, X[k + 13], S21, 0xa9e3e905);
        d = GG(d, a, b, c, X[k + 2], S22, 0xfcefa3f8);
        c = GG(c, d, a, b, X[k + 7], S23, 0x676f02d9);
        b = GG(b, c, d, a, X[k + 12], S24, 0x8d2a4c8a);

        a = HH(a, b, c, d, X[k + 5], S31, 0xfffa3942);
        d = HH(d, a, b, c, X[k + 8], S32, 0x8771f681);
        c = HH(c, d, a, b, X[k + 11], S33, 0x6d9d6122);
        b = HH(b, c, d, a, X[k + 14], S34, 0xfde5380c);
        a = HH(a, b, c, d, X[k + 1], S31, 0xa4beea44);
        d = HH(d, a, b, c, X[k + 4], S32, 0x4bdecfa9);
        c = HH(c, d, a, b, X[k + 7], S33, 0xf6bb4b60);
        b = HH(b, c, d, a, X[k + 10], S34, 0xbebfbc70);
        a = HH(a, b, c, d, X[k + 13], S31, 0x289b7ec6);
        d = HH(d, a, b, c, X[k + 0], S32, 0xeaa127fa);
        c = HH(c, d, a, b, X[k + 3], S33, 0xd4ef3085);
        b = HH(b, c, d, a, X[k + 6], S34, 0x04881d05);
        a = HH(a, b, c, d, X[k + 9], S31, 0xd9d4d039);
        d = HH(d, a, b, c, X[k + 12], S32, 0xe6db99e5);
        c = HH(c, d, a, b, X[k + 15], S33, 0x1fa27cf8);
        b = HH(b, c, d, a, X[k + 2], S34, 0xc4ac5665);

        a = II(a, b, c, d, X[k + 0], S41, 0xf4292244);
        d = II(d, a, b, c, X[k + 7], S42, 0x432aff97);
        c = II(c, d, a, b, X[k + 14], S43, 0xab9423a7);
        b = II(b, c, d, a, X[k + 5], S44, 0xfc93a039);
        a = II(a, b, c, d, X[k + 12], S41, 0x655b59c3);
        d = II(d, a, b, c, X[k + 3], S42, 0x8f0ccc92);
        c = II(c, d, a, b, X[k + 10], S43, 0xffeff47d);
        b = II(b, c, d, a, X[k + 1], S44, 0x85845dd1);
        a = II(a, b, c, d, X[k + 8], S41, 0x6fa87e4f);
        d = II(d, a, b, c, X[k + 15], S42, 0xfe2ce6e0);
        c = II(c, d, a, b, X[k + 6], S43, 0xa3014314);
        b = II(b, c, d, a, X[k + 13], S44, 0x4e0811a1);
        a = II(a, b, c, d, X[k + 4], S41, 0xf7537e82);
        d = II(d, a, b, c, X[k + 11], S42, 0xbd3af235);
        c = II(c, d, a, b, X[k + 2], S43, 0x2ad7d2bb);
        b = II(b, c, d, a, X[k + 9], S44, 0xeb86d391);

        a = add(a, AA);
        b = add(b, BB);
        c = add(c, CC);
        d = add(d, DD);
    }

    const res = new Uint8Array(16);
    res[0] = (a) & 0xFF;
    res[1] = (a >>> 8) & 0xFF;
    res[2] = (a >>> 16) & 0xFF;
    res[3] = (a >>> 24) & 0xFF;
    res[4] = (b) & 0xFF;
    res[5] = (b >>> 8) & 0xFF;
    res[6] = (b >>> 16) & 0xFF;
    res[7] = (b >>> 24) & 0xFF;
    res[8] = (c) & 0xFF;
    res[9] = (c >>> 8) & 0xFF;
    res[10] = (c >>> 16) & 0xFF;
    res[11] = (c >>> 24) & 0xFF;
    res[12] = (d) & 0xFF;
    res[13] = (d >>> 8) & 0xFF;
    res[14] = (d >>> 16) & 0xFF;
    res[15] = (d >>> 24) & 0xFF;

    return res;
}

function cmn(q, a, b, x, s, t) {
    a = add(add(a, q), add(x, t));
    return add((a << s) | (a >>> (32 - s)), b);
}

function FF(a, b, c, d, x, s, t) {
    return cmn((b & c) | ((~b) & d), a, b, x, s, t);
}

function GG(a, b, c, d, x, s, t) {
    return cmn((b & d) | (c & (~d)), a, b, x, s, t);
}

function HH(a, b, c, d, x, s, t) {
    return cmn(b ^ c ^ d, a, b, x, s, t);
}

function II(a, b, c, d, x, s, t) {
    return cmn(c ^ (b | (~d)), a, b, x, s, t);
}

function add(x, y) {
    const lsw = (x & 0xFFFF) + (y & 0xFFFF);
    const msw = (x >> 16) + (y >> 16) + (lsw >> 16);
    return (msw << 16) | (lsw & 0xFFFF);
}
