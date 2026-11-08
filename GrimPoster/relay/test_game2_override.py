"""Regression tests for scheduled, no-event override, ack, and duplicate handling."""
import asyncio
import io
import sys
import types
from PIL import Image
from aiohttp import FormData
from aiohttp.test_utils import TestClient, TestServer

notifier = types.ModuleType('grim_relay_notifier')
notifier.notify_successful_reveal = lambda *args, **kwargs: '8888'
notifier.game2_schedule_status = lambda: 'scheduled'
notifier.notify_unscheduled_game2 = lambda message_id, payload: '9999'
sys.modules['grim_relay_notifier'] = notifier
import app


def sample_png():
    result = io.BytesIO()
    Image.new('RGB', (80,60), (65,24,80)).save(result, format='PNG')
    return result.getvalue()


async def make_proof(client):
    form=FormData()
    form.add_field('kind','stage')
    form.add_field('file',sample_png(),filename='grim.png',content_type='image/png')
    resp=await client.post('/upload',data=form,headers={'X-Grim-Stage':'1'})
    assert resp.status==200,(resp.status,await resp.text())
    stage=await resp.json()
    resp=await client.post('/upload',json={'kind':'commit','stage_id':stage['stage_id'],
               'stage_token':stage['stage_token'],'content':'Test result'})
    assert resp.status==200,(resp.status,await resp.text())
    result=await resp.json()
    return dict(kind='announce_game2',request_id=result['game2_request_id'], token=result['game2_token'])

async def main():
    original_discord=app.send_to_discord
    notices=[]; posts=[]
    async def discord_stub(content,image,filename,media_type,return_message_id=False):
        posts.append(content)
        return str(1000+len(posts)) if return_message_id else None
    app.send_to_discord=discord_stub
    app.consume_rate_limit=lambda *args, **kwargs: True  # Tests do not simulate wall time.
    app.event_notifier.notify_successful_reveal=lambda mid,event_id=None: notices.append(('scheduled',mid)) or '8888'
    app.event_notifier.notify_unscheduled_game2=lambda mid,payload: notices.append(('manual',mid,payload)) or '9999'
    status=['scheduled']
    app.event_notifier.game2_schedule_status=lambda: status[0]
    try:
      async with TestServer(app.create_app()) as server:
        async with TestClient(server) as client:
          proof=await make_proof(client)
          resp=await client.post('/upload',json=proof)
          data=await resp.json()
          assert resp.status==200 and data=={'ok':True,'event_status':'scheduled','discord_message_id':'8888'},data
          assert notices==[('scheduled','1001')]
          resp=await client.post('/upload',json=proof)
          assert (await resp.json())['already_announced']
          assert len(notices)==1,'Must post once'

          status[0]='no_event'
          proof2=await make_proof(client)
          resp=await client.post('/upload',json=proof2)
          data=await resp.json()
          assert resp.status==200 and data['confirmation_required'] is True
          assert len(notices)==1,'No event must never auto-post'
          proof2['confirm_unscheduled']=True
          resp=await client.post('/upload',json=proof2)
          data=await resp.json()
          assert resp.status==200 and data['discord_message_id']=='9999'
          assert len(notices)==2 and notices[-1][0]=='manual'
          assert notices[-1][2]['content'] == ('🦈 **Another game of Blood on the Clocktower?**\n\n'
                    'The next game is being organised!\n\n'
                    "If you're around and fancy joining another game, come along!")
          assert notices[-1][2]['allowed_mentions'] == {'parse': []}
          resp=await client.post('/upload',json=proof2)
          assert (await resp.json())['already_announced']
          assert len(notices)==2

          # Invalid token cannot preflight or override.
          bad=proof2.copy();bad['token']='bad'
          resp=await client.post('/upload',json=bad)
          assert resp.status==404

          # Outage: don't pretend it means 'no event'.
          status[0]='unknown'
          proof3=await make_proof(client)
          resp=await client.post('/upload',json=proof3)
          assert resp.status==502
          assert len(notices)==2

          # Silent notifier: never acknowledge an absent Discord announcement.
          status[0]='scheduled'
          app.event_notifier.notify_successful_reveal=lambda mid,event_id=None: None
          resp=await client.post('/upload',json=proof3)
          assert resp.status==502
          assert len(notices)==2

          # A failure must not consume a proof; retry can work.
          app.event_notifier.notify_successful_reveal=lambda mid,event_id=None: '7777'
          resp=await client.post('/upload',json=proof3)
          assert resp.status==200 and (await resp.json())['discord_message_id']=='7777'
          assert len(posts)==3,'Only screenshots should hit the Grimoire webhook'
      print('Relay: scheduled, confirmation, override, idempotency, outage, silent return, retry PASS')
    finally:
      app.send_to_discord=original_discord

if __name__=='__main__':asyncio.run(main())
