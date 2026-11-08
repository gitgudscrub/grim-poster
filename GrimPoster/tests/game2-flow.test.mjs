import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source=fs.readFileSync('src/grim-poster.js','utf8');
function section(start,end){const a=source.indexOf(start),b=source.indexOf(end,a+start.length);assert(a>=0&&b>a,`Missing ${start}`);return source.slice(a,b);}
assert.match(source,/MOD_VERSION = "0\.2\.0"/);
assert(!source.includes('confirmCompletedReveal()'));
assert(!source.includes('Post Grim + notify Game 2'));
assert(!source.includes('id="gp-auto-post"'));

async function game2Case(scenario) {
  let created, clicks={}, removed=false;
  const controls={
    '#gp-game2-send':{disabled:false,addEventListener(_t,f){clicks.send=f;}},
    '#gp-game2-dismiss':{addEventListener(_t,f){clicks.dismiss=f;}},
  };
  const doc={
    createElement(){created={id:'',className:'',innerHTML:'',querySelector(id){return controls[id];},remove(){removed=true;}};return created;},
    getElementById(){return created&&!removed?created:null;},
    body:{appendChild(){}},
  };
  const state={revealCycle:1,game2Proof:{request_id:'abc',token:'secret'},game2Sending:false,game2PromptCycle:null};
  const sent=[], notice=[];
  const ctx={expirePending:async()=>{},recoveryExpired:()=>false,document:doc,state,UPLOAD_RELAY_URL:'/upload',toast:(...x)=>notice.push(x),
    fetch:async (url,opts)=>{
      const body=JSON.parse(opts.body); sent.push(body);
      let response;
      if (scenario==='unscheduled-yes' || scenario==='unscheduled-no') {
        response = sent.length === 1
          ? {ok:false,confirmation_required:true,event_status:'no_event'}
          : {ok:true,discord_message_id:'2222'};
      } else if (scenario==='service-down') {
        return {ok:false,status:502,json:async()=>({error:'Unavailable'})};
      } else if (scenario==='false-success') {
        response={ok:true};
      } else response={ok:true,discord_message_id:'1111'};
      return {ok:true,json:async()=>response};
    },
  };
  vm.createContext(ctx);
  vm.runInContext(section('  function dismissGame2Prompt()', '  async function directRelayRequest(')+'\nglobalThis.showGame2Prompt=showGame2Prompt;',ctx);
  ctx.askUnscheduledGame2Confirmation=async()=>scenario==='unscheduled-yes';
  ctx.showGame2Prompt(1);
  assert.match(created.innerHTML,/id="gp-game2-send"/);
  await clicks.send();
  if (scenario==='unscheduled-yes') {
    assert.equal(sent.length,2); assert.equal(sent[0].confirm_unscheduled,false);
    assert.equal(sent[1].confirm_unscheduled,true); assert(removed);
    assert.equal(notice.at(-1)[1],'success');
  } else if (scenario==='unscheduled-no') {
    assert.equal(sent.length,1); assert.equal(sent[0].confirm_unscheduled,false);
    assert.equal(removed,false); assert(state.game2Proof); assert.equal(controls['#gp-game2-send'].disabled,false);
  } else if (scenario==='service-down' || scenario==='false-success') {
    assert.equal(sent.length,1); assert.equal(removed,false);assert(state.game2Proof);
    assert.equal(notice.at(-1)[1],'error');
  } else {
    assert.equal(sent.length,1); assert.equal(sent[0].kind,'announce_game2');
    assert.equal(sent[0].request_id,'abc');assert.equal(sent[0].token,'secret');
    assert.equal(sent[0].confirm_unscheduled,false);
    assert.equal(state.game2Proof,null);assert(removed);
  }
  if (!removed) { clicks.dismiss(); assert(removed); }
}
async function testPrompt() {
  await game2Case('scheduled');
  await game2Case('unscheduled-yes');
  await game2Case('unscheduled-no');
  await game2Case('service-down');
  await game2Case('false-success');
}
async function testClose() {
  const state={revealCycle:4, config:{enabled:true},pending:{id:'grim',cycle:4},capturePromise:null};
  const actions=[];let click;
  const ctx={expirePending:async()=>{},recoveryExpired:()=>false,state,document:{addEventListener(_type,fn){click=fn;}},findRevealPrompt:()=>null,
    revealHasStarted:()=>true,clearPending:async()=>{},persistPending:async x=>{state.pending=x;},
    showGame2Prompt:cycle=>actions.push(['prompt',cycle]),uploadPending:async force=>{actions.push(['post',force]);return true;},
    toast(){},setTimeout(fn){queueMicrotask(fn);}};
  vm.createContext(ctx);
  vm.runInContext(section('  function installCloseHandler()', '  function checkRevealState()')+'\nglobalThis.installCloseHandler=installCloseHandler;',ctx);
  ctx.installCloseHandler();
  click({target:{closest:s=>s.startsWith('#reveal ') ? {}:null}});
  await new Promise(r=>setTimeout(r,10));
  assert.equal(state.pending.completedReveal,true);
  assert.deepEqual(actions,[['prompt',4],['post',true]]);
  assert(!('notifyGame2' in state.pending));
}
async function testUpload() {
  const actions=[];
  const state={revealCycle:1,uploading:false,config:{enabled:true},pending:{id:'grim',cycle:1,blob:{size:10},filename:'grim.png'},
    stagePromise:Promise.resolve(),staged:{cycle:1,id:'stage',token:'stage-token'}};
  const ctx={expirePending:async()=>{},recoveryExpired:()=>false,state,DISCORD_FILE_LIMIT:99e6,isDesktopApp:()=>true,updatePendingControls(){},toast(){},console,
    buildMessage:()=> 'text',commitStaged:async()=>({ok:true,game2_request_id:'id1',game2_token:'proof1'}),
    relayRequest:async()=>{throw Error('Unexpected fallback')},clearPending:async()=>{state.pending=null;},updateStatusButton(){}};
  vm.createContext(ctx);
  vm.runInContext(section('  async function uploadPending(', '  function downloadPending()')+'\nglobalThis.uploadPending=uploadPending;',ctx);
  assert(await ctx.uploadPending(true));
  assert.equal(state.pending,null);
  assert.equal(state.game2Proof.request_id,'id1');
  assert.equal(state.game2Proof.token,'proof1');
}
await testPrompt();await testClose();await testUpload();
console.log('Game 2: separate bottom-right prompt, X dismissal, auto-post, token-protected one-time announce passed');
