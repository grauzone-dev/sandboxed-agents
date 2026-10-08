package livesuite

const identityObservationScript = `
const fs = require('node:fs');
function identity(pid) {
  const text = fs.readFileSync('/proc/' + pid + '/status', 'utf8');
  const fields = {};
  for (const line of text.split('\n')) {
    const match = /^(Uid|Gid|NoNewPrivs):\s+(.*)$/.exec(line);
    if (match) fields[match[1]] = match[2].trim().split(/\s+/).map(Number);
  }
  if (!fields.Uid || !fields.Gid || !fields.NoNewPrivs) throw Error('proc-status');
  return {uid: fields.Uid, gid: fields.Gid, no_new_privileges: fields.NoNewPrivs[0]};
}
`

const agentObservationScript = identityObservationScript + `
process.stdout.write(JSON.stringify({...identity('self'),name:require('node:os').userInfo().username,user_namespace:fs.readlinkSync('/proc/self/ns/user')}));
`

const gatewayObservationScript = `
function gateway() {
  const routes = fs.readFileSync('/proc/net/route', 'utf8').trim().split('\n').slice(1);
  const defaults = routes.map(line => line.trim().split(/\s+/)).filter(parts => parts[1] === '00000000' && (parseInt(parts[3],16) & 3) === 3);
  if (defaults.length !== 1 || !/^[0-9A-Fa-f]{8}$/.test(defaults[0][2])) throw Error('default-gateway');
  return defaults[0][2].match(/../g).reverse().map(value => parseInt(value,16)).join('.');
}
`

const kernelObservationScript = identityObservationScript + gatewayObservationScript + `
const cp = require('node:child_process');
const os = require('node:os');
const path = require('node:path');
async function managerIdentity() {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(),'live-manager-'));
  const fifo = path.join(directory,'stdout');
  let fd,child;
  try {
    cp.execFileSync('mkfifo',[fifo]);
    fd = fs.openSync(fifo,fs.constants.O_RDWR | fs.constants.O_NONBLOCK);
    const fill = Buffer.alloc(4096);
    for (;;) {
      try { fs.writeSync(fd,fill); } catch(error) { if(error.code === 'EAGAIN') break; throw error; }
    }
    const executable = '/usr/local/bin/sandboxed-agents-manager';
    child = cp.spawn('/bin/sh',['-c','exec "$1" version >"$2"','live-manager',executable,fifo],{stdio:'ignore'});
    const deadline = Date.now()+5000;
    for (;;) {
      if(child.exitCode !== null || child.signalCode !== null) throw Error('manager-exited');
      try {
        if(fs.readlinkSync('/proc/'+child.pid+'/exe') === executable) {
          if(fs.readlinkSync('/proc/'+child.pid+'/ns/user') !== fs.readlinkSync('/proc/1/ns/user')) throw Error('manager-user-namespace');
          return identity(child.pid);
        }
      } catch(error) { if(error.code !== 'ENOENT') throw error; }
      if(Date.now() >= deadline) throw Error('manager-observation-timeout');
      await new Promise(resolve => setTimeout(resolve,10));
    }
  } finally {
    if(child && child.exitCode === null && child.signalCode === null) {
      const exited = new Promise(resolve => child.once('exit',resolve));
      child.kill('SIGKILL');
      await exited;
    }
    if(fd !== undefined) fs.closeSync(fd);
    fs.rmSync(directory,{recursive:true,force:true});
  }
}
function cgroupDirectory() {
  const unified = fs.readFileSync('/proc/self/cgroup','utf8').trim().split('\n').filter(line => line.startsWith('0::'));
  if(unified.length !== 1) throw Error('cgroup-v2');
  const mounts = fs.readFileSync('/proc/self/mountinfo','utf8').trim().split('\n').filter(line => line.split(' - ')[1]?.startsWith('cgroup2 '));
  if(mounts.length !== 1) throw Error('cgroup-mount');
  const fields = mounts[0].split(' - ')[0].split(' ');
  const decode = value => value.replace(/\\([0-7]{3})/g,(_,digits) => String.fromCharCode(parseInt(digits,8)));
  const relative = path.posix.relative(decode(fields[3]),unified[0].slice(3));
  if(relative === '..' || relative.startsWith('../')) throw Error('cgroup-path');
  return path.posix.join(decode(fields[4]),relative);
}
(async() => {
	const namespace=fs.readlinkSync('/proc/1/ns/user');
	if(fs.readlinkSync('/proc/self/ns/user') !== namespace) throw Error('probe-user-namespace');
  const cgroup = cgroupDirectory();
  const read = name => fs.readFileSync(path.join(cgroup,name),'utf8').trim();
  const shm = fs.statfsSync('/dev/shm');
  process.stdout.write(JSON.stringify({
    start:identity(1),manager:await managerIdentity(),memory:read('memory.max'),cpu:read('cpu.max'),pids:read('pids.max'),shm:shm.bsize*shm.blocks,gateway:gateway(),user_namespace:namespace
  }));
})().catch(error => {process.stderr.write(error.message+'\n');process.exitCode=1;});
`

const gatewayListenerScript = `
const fs=require('node:fs');
const net=require('node:net');
const token=process.argv[1];
const server=net.createServer(socket => socket.end(token));
server.on('error',error => {process.stderr.write(error.message+'\n');process.exit(1);});
server.listen(0,'0.0.0.0',() => fs.writeFileSync('/tmp/live-gateway-port',String(server.address().port)));
setTimeout(() => process.exit(1),120000);
`

const gatewayClientScript = `
const fs=require('node:fs');
const net=require('node:net');
` + gatewayObservationScript + `
const [address,port,token,mode]=process.argv.slice(1);
if(gateway() !== address) process.exit(1);
const socket=net.createConnection({host:address,port:Number(port)});
let data='';
let settled=false;
function finish(ok) {if(settled)return;settled=true;socket.destroy();process.stdout.write(ok?'pass':'fail');process.exitCode=ok?0:1;}
socket.setTimeout(2000,() => finish(mode==='denied'));
socket.on('connect',() => {if(mode==='denied')finish(false);});
socket.on('data',chunk => {data+=chunk;if(data.length>token.length)finish(false);});
socket.on('end',() => finish(mode==='allowed' && data===token));
socket.on('error',error => finish(mode==='denied' && ['ECONNREFUSED','ETIMEDOUT','EHOSTUNREACH','ENETUNREACH'].includes(error.code)));
`
