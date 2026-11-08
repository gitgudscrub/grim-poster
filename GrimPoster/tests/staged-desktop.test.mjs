import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const source=fs.readFileSync('src/grim-poster.js','utf8');
function section(start,end){let a=source.indexOf(start),b=source.indexOf(end,a+start.length);assert(a>=0&&b>a);return source.slice(a,b);}
const code = section('  async function prepareStage(', '  function buildMessage(')+section('  async function uploadPending(', '  function downloadPending(');
async function runCase(mode){
  const logs=[];
  const capture={id:'cap',cycle:1,blob:{size:100},filename:'grim.png',completedReveal:true,capturedAt:5000};
  const state={revealCycle:1,stagePromise:null,staged:null,uploading:false,capturePromise:null,pending:capture,config:{enabled:true,autoPost:true}};
  let fetches=0;
  class FormData{ append(){} }
  const ctx={state,console,FormData,DISCORD_FILE_LIMIT:9_961_472,MOD_NAME:'Grim Poster',UPLOAD_RELAY_URL:'/upload',
    isDesktopApp:()=>true,AbortController,setTimeout,clearTimeout,buildMessage:()=> 'Test Grim',
    getConfig:async()=>({autoPost:true}),updatePendingControls(){},toast(){},updateStatusButton(){},
    clearPending:async()=>{state.pending=null;},
    relayRequest:async x=>{logs.push('legacy');return {ok:true};},
    fetch:async (url, opts)=>{
      assert.equal(url,'/upload');fetches++;
      const kind=opts.headers?.['X-Grim-Stage']?'stage':'commit';logs.push(kind);
      if(kind==='stage') return {ok:true,json:async()=>({stage_id:'id',stage_token:'secret'})};
      if(mode==='expire') return {ok:false,status:404};
      if(mode==='error') return {ok:false,status:502,json:async()=>({error:'Discord temporary failure'})};
      return {ok:true,json:async()=>({ok:true})};
    },
  };
  vm.createContext(ctx);vm.runInContext(code+'\nglobalThis.prepareStage=prepareStage;globalThis.uploadPending=uploadPending;',ctx);
  state.stagePromise=ctx.prepareStage({size:200},1);
  await state.stagePromise;
  assert.deepEqual(logs,['stage'],'staging must never cause Discord posting');
  const posted=await ctx.uploadPending(true,false);
  if(mode==='success'){
    assert(posted);assert.deepEqual(logs,['stage','commit']);assert.equal(state.pending,null);
  }else if(mode==='expire'){
    assert(posted);assert.deepEqual(logs,['stage','commit','legacy']);assert.equal(state.pending,null);
  }else{
    assert.equal(posted,false);assert.deepEqual(logs,['stage','commit']);assert.equal(state.pending,capture);
  }
}
await runCase('success');await runCase('expire');await runCase('error');
console.log('Desktop staging/commit/fallback safety tests passed');
