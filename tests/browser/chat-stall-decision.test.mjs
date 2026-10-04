import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

// Evaluate the same pure decision function used by the browser with a fixed clock.
const source=await readFile('internal/controller/web/chat.js','utf8');
const start=source.indexOf(' function deriveStallHint(');
const end=source.indexOf('\n const elapsedLabel=',start);
assert.ok(start>0&&end>start);
const deriveStallHint=vm.runInNewContext(source.slice(start,end)+'\nderiveStallHint');
const now=Date.parse('2026-10-04T12:00:00Z');
const at=minutes=>new Date(now-minutes*60000).toISOString();
const box=extra=>({activityState:'working',agentBusySince:at(192),messages:[{direction:'user',state:'delivered',createdAt:at(192)}],...extra});

test('stall hint uses liveness after the current turn begins',()=>{
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(1),activityStatusSource:'fallback'}),now).kind,'working','healthy three-hour task is neutral');
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(14),activityStatusSource:'stale'}),now).kind,'stalled','14 minutes without evidence warns');
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(14),mascotObservedAt:at(1),activityStatusSource:''}),now).kind,'working','new message header beats an older batch');
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(1),activityStatusSource:'quiet'}),now).kind,'stalled','quiet keepalive is not work');
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(14),lastActivityPhraseAt:at(1),activityStatusSource:'stale'}),now).kind,'working','phrase change is evidence');
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(14),activityStatusSource:'stale',messages:[{direction:'user',createdAt:at(192)},{direction:'system',text:'MCP · tool call',createdAt:at(1)}]}),now).kind,'working','tool-call message is evidence');
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(14),activityStatusSource:'stale',streaming:true}),now).kind,'working','streaming is current evidence');
 assert.equal(deriveStallHint(box({lastMascotObservedAt:at(14),activityStatusSource:'stale'}),now,at(1)),null,'pending send starts a new turn');
 assert.equal(deriveStallHint(box({activityState:'idle'}),now),null,'idle has no stall hint');
 assert.equal(deriveStallHint(box({agentBusySince:at(9),messages:[{direction:'user',createdAt:at(9)}]}),now),null,'working under ten minutes is quiet');
 assert.equal(deriveStallHint(box({agentBusySince:at(10),messages:[{direction:'user',createdAt:at(10)}]}),now).kind,'stalled','warning begins at ten minutes');
});
