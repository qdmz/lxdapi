/**
 * LXD Panel - Mock Backend (zero dependency)
 * Serves the static frontend + implements all APIs the panel needs,
 * so the UI can be previewed without a real LXD host.
 *   node mock-server.js   ->  http://127.0.0.1:8099
 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const zlib = require('zlib');

const PORT = process.env.PORT || 8099;
const ROOT = __dirname;

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'application/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.json': 'application/json; charset=utf-8'
};

// ---------------- demo state ----------------
const state = {
  status: 'running',
  ipv4: [
    { ip_address: '198.51.100.24', status: 'bound', created_at: Date.now() - 86400000 * 12 },
    { ip_address: '198.51.100.25', status: 'allocated', created_at: Date.now() - 86400000 * 3 }
  ],
  ipv6: [
    { ip_address: '2001:db8:1::a1', status: 'bound', created_at: Date.now() - 86400000 * 12 },
    { ip_address: '2001:db8:1::a2', status: 'allocated', created_at: Date.now() - 86400000 * 2 }
  ],
  mappings: {
    ipv4: [
      { id: 1, public_port: 2201, public_port_end: 2201, container_port: 22, container_port_end: 22, protocol: 'tcp', description: 'SSH', public_ip: '198.51.100.24', interface: 'eth0', status: 'active', created_at: Date.now() - 86400000 * 10 },
      { id: 2, public_port: 8080, public_port_end: 8080, container_port: 80, container_port_end: 80, protocol: 'tcp', description: 'Web', public_ip: '198.51.100.24', interface: 'eth0', status: 'active', created_at: Date.now() - 86400000 * 5 }
    ],
    ipv6: [
      { id: 3, public_port: 8443, public_port_end: 8443, container_port: 443, container_port_end: 443, protocol: 'tcp', description: 'HTTPS', public_ip: '2001:db8:1::a1', interface: 'eth0', status: 'active', created_at: Date.now() - 86400000 * 4 }
    ]
  },
  proxies: [
    { id: 1, domain: 'demo.example.com', protocol: 'https', target_port: 8080, enable_ssl: true, status: 'active', description: '主站反代' },
    { id: 2, domain: 'api.example.com', protocol: 'http', target_port: 3000, enable_ssl: false, status: 'active', description: 'API 反代' }
  ],
  dns: ['1.1.1.1', '8.8.8.8', '2606:4700:4700::1111'],
  nextIp: 26,
  nextId: 10
};

const captchas = new Map(); // id -> code
const hashes = new Set();   // verified container hashes
const tokens = new Map();   // token -> {used}

const rnd = (a, b) => a + Math.random() * (b - a);
const clamp = (v, a, b) => Math.max(a, Math.min(b, v));

function containerInfo() {
  const t = Date.now() / 1000;
  const cpu = clamp(22 + 14 * Math.sin(t / 9) + rnd(-3, 3), 2, 96);
  const memTotal = 2 * 1024 * 1024 * 1024;
  const memUsed = clamp(memTotal * (0.42 + 0.06 * Math.sin(t / 13)), 1, memTotal);
  const diskTotal = 20 * 1024 * 1024 * 1024;
  const diskUsed = diskTotal * 0.37;
  return {
    name: 'demo-container',
    status: state.status,
    image: 'ubuntu/22.04 (cloud)',
    password: 'P@ssw0rd-demo',
    hash: 'lxdp-demo-hash-8f3c21',
    ipv4: state.ipv4.filter((x) => x.status === 'bound').map((x) => x.ip_address),
    ipv6: state.ipv6.filter((x) => x.status === 'bound').map((x) => x.ip_address),
    cpu: 2,
    memory: '2GB',
    disk: '20GB',
    traffic_limit: 500,
    cpu_usage: Math.round(cpu * 10) / 10,
    memory_usage_raw: Math.round(memUsed),
    disk_usage_raw: Math.round(diskUsed),
    traffic_usage_raw: Math.round((128 + 12 * Math.sin(t / 40)) * 10) / 10,
    v4_port_start: 20000,
    v4_port_end: 30000,
    v6_port_start: 40000,
    v6_port_end: 50000,
    ipv4_pool_limit: 5,
    ipv4_mapping_limit: 10,
    ipv6_pool_limit: 5,
    ipv6_mapping_limit: 10,
    reverse_proxy_limit: 10,
    allow_container_release_ipv4: true,
    allow_container_release_ipv6: true
  };
}

// ---------------- tiny PNG captcha ----------------
const FONT = {
  '0': ['01110', '10001', '10011', '10101', '11001', '10001', '01110'],
  '1': ['00100', '01100', '00100', '00100', '00100', '00100', '01110'],
  '2': ['01110', '10001', '00001', '00010', '00100', '01000', '11111'],
  '3': ['11111', '00010', '00100', '00010', '00001', '10001', '01110'],
  '4': ['00010', '00110', '01010', '10010', '11111', '00010', '00010'],
  '5': ['11111', '10000', '11110', '00001', '00001', '10001', '01110'],
  '6': ['00110', '01000', '10000', '11110', '10001', '10001', '01110'],
  '7': ['11111', '00001', '00010', '00100', '01000', '01000', '01000'],
  '8': ['01110', '10001', '10001', '01110', '10001', '10001', '01110'],
  '9': ['01110', '10001', '10001', '01111', '00001', '00010', '01100']
};

function crc32(buf) {
  let c, crc = 0xffffffff;
  for (let n = 0; n < buf.length; n++) {
    c = (crc ^ buf[n]) & 0xff;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    crc = (crc >>> 8) ^ c;
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body), 0);
  return Buffer.concat([len, body, crc]);
}

function png(width, height, pixels) {
  const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; ihdr[9] = 2; ihdr[10] = 0; ihdr[11] = 0; ihdr[12] = 0;
  const raw = Buffer.alloc(height * (1 + width * 3));
  for (let y = 0; y < height; y++) {
    const off = y * (1 + width * 3);
    raw[off] = 0;
    pixels.copy(raw, off + 1, y * width * 3, (y + 1) * width * 3);
  }
  return Buffer.concat([
    sig,
    chunk('IHDR', ihdr),
    chunk('IDAT', zlib.deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0))
  ]);
}

function captchaPng(code) {
  const scale = 6, pad = 10, gap = 5;
  const w = pad * 2 + code.length * (5 * scale + gap) - gap;
  const h = pad * 2 + 7 * scale;
  const px = Buffer.alloc(w * h * 3);
  const set = (x, y, r, g, b) => {
    if (x < 0 || y < 0 || x >= w || y >= h) return;
    const i = (y * w + x) * 3;
    px[i] = r; px[i + 1] = g; px[i + 2] = b;
  };
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) set(x, y, 244, 246, 250);
  // noise dots
  for (let i = 0; i < 160; i++) set(Math.floor(Math.random() * w), Math.floor(Math.random() * h), 200, 205, 215);
  // digits
  for (let d = 0; d < code.length; d++) {
    const glyph = FONT[code[d]];
    const ox = pad + d * (5 * scale + gap);
    const oy = pad + Math.floor(rnd(-2, 2));
    for (let gy = 0; gy < 7; gy++) {
      for (let gx = 0; gx < 5; gx++) {
        if (glyph[gy][gx] !== '1') continue;
        for (let sy = 0; sy < scale; sy++) {
          for (let sx = 0; sx < scale; sx++) {
            set(ox + gx * scale + sx, oy + gy * scale + sy, 30, 58, 95);
          }
        }
      }
    }
  }
  return png(w, h, px).toString('base64');
}

// ---------------- helpers ----------------
function json(res, code, obj, status) {
  const body = JSON.stringify(Object.assign({ code }, obj));
  res.writeHead(status || 200, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body)
  });
  res.end(body);
}
const ok = (res, data, msg) => json(res, 200, { data: data === undefined ? null : data, msg: msg || 'ok' });
const fail = (res, msg, code, status) => json(res, code || 400, { msg }, status || 200);

function readBody(req) {
  return new Promise((resolve) => {
    let b = '';
    req.on('data', (c) => { b += c; if (b.length > 1e6) req.destroy(); });
    req.on('end', () => { try { resolve(b ? JSON.parse(b) : {}); } catch (e) { resolve({}); } });
  });
}

function serveStatic(req, res, urlPath) {
  let rel = decodeURIComponent(urlPath.split('?')[0]);
  if (rel === '/' || rel === '') rel = '/login.html';
  const file = path.join(ROOT, path.normalize(rel).replace(/^([/\\])+/, ''));
  if (!file.startsWith(ROOT)) { res.writeHead(403); return res.end('forbidden'); }
  fs.stat(file, (err, st) => {
    if (err || !st.isFile()) {
      res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
      return res.end('404');
    }
    res.writeHead(200, {
      'Content-Type': MIME[path.extname(file).toLowerCase()] || 'application/octet-stream',
      'Content-Length': st.size,
      'Cache-Control': 'no-cache'
    });
    fs.createReadStream(file).pipe(res);
  });
}

function authHash(req) {
  const h = req.headers['x-container-hash'];
  return h && (hashes.has(h) || h.length >= 4) ? h : '';
}

// ---------------- WebSocket (minimal, no deps) ----------------
function wsSend(socket, str) {
  const payload = Buffer.from(str, 'utf8');
  let header;
  if (payload.length < 126) {
    header = Buffer.from([0x81, payload.length]);
  } else if (payload.length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x81; header[1] = 126; header.writeUInt16BE(payload.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x81; header[1] = 127; header.writeBigUInt64BE(BigInt(payload.length), 2);
  }
  socket.write(Buffer.concat([header, payload]));
}

const CMDS = {
  ls: 'bin  boot  dev  etc  home  lib  media  mnt  opt  proc  root  run  sbin  srv  tmp  usr  var',
  pwd: '/root',
  whoami: 'root',
  'uname -a': 'Linux demo-container 5.15.0-x #1 SMP PREEMPT x86_64 GNU/Linux',
  'df -h': 'Filesystem      Size  Used Avail Use%\noverlay          20G  7.4G   12G  38% /',
  'free -h': '               total        used        free\nMem:           2.0Gi       860Mi       1.1Gi',
  uptime: ' up 12 days,  3:41,  0 users,  load average: 0.21, 0.18, 0.14',
  help: '可用: ls, pwd, whoami, uname -a, df -h, free -h, uptime, neofetch, clear',
  neofetch: 'root@demo-container\n---------------\nOS: Ubuntu 22.04 LTS\nKernel: 5.15.0\nCPU: 2 vCore\nMemory: 860MiB / 2.0GiB'
};

function handleConsole(socket, token) {
  const t = tokens.get(token);
  if (!t || t.used) { wsSend(socket, 'token invalid\r\n'); return socket.end(); }
  t.used = true;
  let line = '';
  wsSend(socket, '\r\n\x1b[32m已连接到 demo-container（模拟终端）\x1b[0m\r\n输入 help 查看可用命令。\r\n\r\n');
  wsSend(socket, 'root@demo-container:~# ');

  socket.on('data', (chunk) => {
    // decode frames (client frames are masked)
    let buf = chunk;
    while (buf.length >= 2) {
      const b0 = buf[0], b1 = buf[1];
      const opcode = b0 & 0x0f;
      let len = b1 & 0x7f;
      let off = 2;
      if (len === 126) { len = buf.readUInt16BE(2); off = 4; }
      else if (len === 127) { len = Number(buf.readBigUInt64BE(2)); off = 10; }
      const masked = (b1 & 0x80) !== 0;
      const mask = masked ? buf.slice(off, off + 4) : null;
      if (masked) off += 4;
      if (buf.length < off + len) break;
      const payload = Buffer.from(buf.slice(off, off + len));
      if (masked) for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
      buf = buf.slice(off + len);

      if (opcode === 8) return socket.end();
      if (opcode === 9) continue;
      if (opcode !== 1) continue;

      let msg;
      try { msg = JSON.parse(payload.toString('utf8')); } catch (e) { msg = { data: payload.toString('utf8') }; }
      const input = (msg && msg.data) || '';

      for (const ch of input) {
        if (ch === '\r' || ch === '\n') {
          const cmd = line.trim();
          line = '';
          if (!cmd) { wsSend(socket, '\r\nroot@demo-container:~# '); continue; }
          if (cmd === 'clear') { wsSend(socket, '\x1b[2J\x1b[Hroot@demo-container:~# '); continue; }
          const out = CMDS[cmd];
          wsSend(socket, '\r\n' + (out !== undefined ? out : 'bash: ' + cmd + ': command not found') + '\r\nroot@demo-container:~# ');
        } else if (ch === '\u007f') {
          if (line.length) { line = line.slice(0, -1); wsSend(socket, '\b \b'); }
        } else if (ch === '\u0003') {
          line = '';
          wsSend(socket, '^C\r\nroot@demo-container:~# ');
        } else {
          line += ch;
          wsSend(socket, ch);
        }
      }
    }
  });
  socket.on('close', () => {});
  socket.on('error', () => {});
}

// ---------------- server ----------------
const server = http.createServer(async (req, res) => {
  const u = new URL(req.url, 'http://localhost');
  const p = u.pathname;

  if (p.startsWith('/api/')) {
    const hash = authHash(req);

    // public
    if (p === '/api/public/brand' && req.method === 'GET') {
      return ok(res, {
        site_name: 'LXD 容器面板',
        page_title: '容器管理',
        footer_text: 'Demo 演示环境 · Mock 后端',
        container_name: 'demo-container',
        logo_url: '',
        favicon_url: ''
      });
    }

    if (p === '/api/container/captcha' && req.method === 'GET') {
      const code = String(Math.floor(1000 + Math.random() * 9000));
      const id = crypto.randomBytes(8).toString('hex');
      captchas.set(id, code);
      return ok(res, { captcha_id: id, image: captchaPng(code) });
    }

    if (p === '/api/container/verify' && req.method === 'POST') {
      const b = await readBody(req);
      const code = captchas.get(b.captcha_id) || Object.values(Object.fromEntries(captchas)).pop();
      if (!b.hash) return fail(res, '请输入容器访问码');
      if (!b.captcha) return fail(res, '请输入验证码');
      // demo mode: accept the generated code, or the universal demo code 1234
      if (code && String(b.captcha) !== code && String(b.captcha) !== '1234') {
        return fail(res, '验证码不正确（演示码：1234）');
      }
      hashes.add(b.hash);
      return ok(res, { hash: b.hash }, '验证成功');
    }

    // authenticated
    if (!hash) return fail(res, '未授权', 401);

    if (p === '/api/container/info' && req.method === 'GET') return ok(res, containerInfo());

    if (p === '/api/container/action' && req.method === 'POST') {
      const b = await readBody(req);
      if (['start', 'stop', 'restart'].indexOf(b.action) < 0) return fail(res, '未知操作');
      if (b.action === 'start') state.status = 'running';
      if (b.action === 'stop') state.status = 'stopped';
      if (b.action === 'restart') state.status = 'running';
      return ok(res, { status: state.status }, '操作成功');
    }

    if (p === '/api/container/ip' && req.method === 'GET') {
      const v = u.searchParams.get('version') === 'v6' ? 'ipv6' : 'ipv4';
      return ok(res, { ipv4: state.ipv4, ipv6: state.ipv6, list: state[v] });
    }
    if (p === '/api/container/ip/allocate' && req.method === 'POST') {
      const b = await readBody(req);
      const v = u.searchParams.get('version') === 'v6' ? 'ipv6' : 'ipv4';
      const n = Math.min(parseInt(b.count, 10) || 1, 5);
      for (let i = 0; i < n; i++) {
        state[v].push({
          ip_address: v === 'ipv4' ? '198.51.100.' + state.nextIp++ : '2001:db8:1::' + state.nextId.toString(16),
          status: 'allocated',
          created_at: Date.now()
        });
      }
      return ok(res, { count: n }, '分配成功');
    }
    if (p === '/api/container/ip/release' && req.method === 'POST') {
      const b = await readBody(req);
      const v = u.searchParams.get('version') === 'v6' ? 'ipv6' : 'ipv4';
      const ip = decodeURIComponent(String(b.ip || ''));
      state[v] = state[v].filter((x) => x.ip_address !== ip);
      return ok(res, null, '已释放');
    }

    if (p === '/api/container/port-mapping' && req.method === 'GET') {
      return ok(res, { ipv4: state.mappings.ipv4, ipv6: state.mappings.ipv6 });
    }
    if (p === '/api/container/port-mapping/allocate' && req.method === 'POST') {
      const b = await readBody(req);
      const v = u.searchParams.get('version') === 'v6' ? 'ipv6' : 'ipv4';
      const item = {
        id: state.nextId++,
        public_port: parseInt(b.public_port, 10) || 20000,
        public_port_end: parseInt(b.public_port_end, 10) || parseInt(b.public_port, 10) || 20000,
        container_port: parseInt(b.container_port, 10) || 80,
        container_port_end: parseInt(b.container_port_end, 10) || parseInt(b.container_port, 10) || 80,
        protocol: b.protocol || 'tcp',
        description: b.description || '',
        public_ip: state[v][0] ? state[v][0].ip_address : '-',
        interface: 'eth0',
        status: 'active',
        created_at: Date.now()
      };
      state.mappings[v].push(item);
      return ok(res, item, '添加成功');
    }
    if (p === '/api/container/port-mapping/release' && req.method === 'POST') {
      const b = await readBody(req);
      const v = u.searchParams.get('version') === 'v6' ? 'ipv6' : 'ipv4';
      const ids = b.ids || [];
      state.mappings[v] = state.mappings[v].filter((m) => ids.indexOf(m.id) < 0);
      return ok(res, null, '已删除');
    }

    if (p === '/api/container/dns' && req.method === 'GET') return ok(res, { dns: state.dns });
    if (p === '/api/container/dns' && req.method === 'PUT') {
      const b = await readBody(req);
      state.dns = b.dns || [];
      return ok(res, { dns: state.dns }, '已保存');
    }

    if (p === '/api/container/nginx/proxies' && req.method === 'GET') return ok(res, state.proxies);
    if (p === '/api/container/nginx/proxies' && req.method === 'POST') {
      const b = await readBody(req);
      const rule = {
        id: state.nextId++,
        domain: b.domain || 'new.example.com',
        protocol: b.protocol || 'http',
        target_port: parseInt(b.target_port, 10) || 80,
        enable_ssl: !!b.enable_ssl,
        status: 'active',
        description: b.description || ''
      };
      state.proxies.push(rule);
      return ok(res, rule, '添加成功');
    }
    if (p.startsWith('/api/container/nginx/proxies/') && req.method === 'DELETE') {
      const id = parseInt(p.split('/').pop(), 10);
      state.proxies = state.proxies.filter((r) => r.id !== id);
      return ok(res, null, '已删除');
    }

    if (p === '/api/container/console/create-token' && req.method === 'POST') {
      const token = crypto.randomBytes(12).toString('hex');
      tokens.set(token, { used: false, at: Date.now() });
      return ok(res, { token, ws_url: '/ws/console?token=' + token });
    }

    if (p === '/api/admin/tasks/detail') {
      return ok(res, { id: 1, type: 'container.sync', status: 'success', message: '同步完成', created_at: Date.now() });
    }

    return fail(res, '接口不存在: ' + p, 404);
  }

  return serveStatic(req, res, req.url);
});

server.on('upgrade', (req, socket) => {
  const u = new URL(req.url, 'http://localhost');
  if (u.pathname !== '/ws/console') { socket.write('HTTP/1.1 404 Not Found\r\n\r\n'); return socket.end(); }
  const key = req.headers['sec-websocket-key'];
  if (!key) return socket.end();
  const accept = crypto.createHash('sha1').update(key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  socket.write(
    'HTTP/1.1 101 Switching Protocols\r\n' +
    'Upgrade: websocket\r\nConnection: Upgrade\r\n' +
    'Sec-WebSocket-Accept: ' + accept + '\r\n\r\n'
  );
  handleConsole(socket, u.searchParams.get('token') || '');
});

server.listen(PORT, '0.0.0.0', () => {
  console.log('LXD Panel mock server: http://127.0.0.1:' + PORT + '  (login code: 1234)');
});
