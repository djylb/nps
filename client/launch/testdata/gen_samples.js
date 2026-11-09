// Prints generator_samples.json: what the web generator produces for a few
// forms. TestGeneratorSamples compares it with this script when node exists.
const path = require('path');
const G = require(path.join(__dirname, '../../../web/static/js/launch-generator.js'));
const carriers = {
  tcp: 'npc://@1.2.3.4:8024?type=tcp', tls: 'npc://@1.2.3.4:8025?type=tls', quic: 'npc://@1.2.3.4:8025?type=quic&alpn=h3',
  ws: 'npc://@1.2.3.4:8026/ws?type=ws', wss: 'npc://@[2001:db8::1]:8027/ws?type=wss',
};
const tunnel = () => ({kind: 'tunnel', vkey: 'v k@x$', password: 'p&"1!', defaultTarget: '10.0.0.1:22', carriers, primary: carriers.tls, bin: './npc'});
const client = () => ({kind: 'client', vkey: 'vkey', carriers, primary: carriers.tcp, bin: './npc'});
function form(ctx, f) { const o = G.defaultForm(ctx); f(o); return o; }
function multi(o) {
  o.pipes = 'multi'; o.scheduler = 'spread_eta';
  o.paths[0].count = '2'; o.paths[0].alpn = 'h2';
  o.paths.push(Object.assign({}, o.paths[0], {carrier: 'wss', alpn: '', count: '', tier: '1', weight: '3', http: 'h2,http/1.1', path: '/w s'}));
  o.paths.push(Object.assign({}, o.paths[0], {carrier: 'quic', alpn: '', count: '', addr: 'b.example', port: '9', standby: true, sni: 'c;d&e'}));
}
function accounts(o) {
  o.access = [{type: 'mixed', ip: '127.0.0.1', port: '1080', target: '', mode: 'auto', local_proxy: false, key: '', auth: [{user: 'a b', pass: 'x:y&z'}, {user: 'c', pass: '2'}]},
    {type: 'tcp', ip: '::1', port: '2000', target: '10.0.0.9:80', mode: 'relay', local_proxy: true, key: '', auth: []}];
}
const cases = {
  'client default': [client(), () => {}],
  'tunnel default': [tunnel(), () => {}],
  'bash paths': [tunnel(), multi],
  'bash accounts': [tunnel(), accounts],
  'cmd paths': [tunnel(), o => { o.os = 'cmd'; multi(o); accounts(o); }],
  'powershell paths': [tunnel(), o => { o.os = 'powershell'; o.bin = 'custom'; o.binPath = 'C:\\My Dir\\npc'; multi(o); accounts(o); }],
  'service named': [tunnel(), o => { o.run = 'service'; o.named = true; o.name = 'ho me'; accounts(o); }],
  'docker': [client(), o => { o.run = 'docker'; o.os = 'cmd'; }],
};
const out = Object.keys(cases).sort().map(name => {
  const [ctx, f] = cases[name], o = form(ctx, f);
  return Object.assign({name, shell: o.os === 'cmd' ? 'cmd' : o.os === 'powershell' ? 'powershell' : 'bash'}, G.outputs(o, ctx));
});
process.stdout.write(JSON.stringify(out, null, 1) + '\n');
