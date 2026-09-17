// A DOM contract test for the exact expression embedded in the Go binary.
// Run: node --test internal/meet/detect_test.cjs
const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const expression = fs.readFileSync(`${__dirname}/detect.js`, 'utf8');
function detect(buttons, origin = 'https://meet.google.com', pathname = '/abc-defg-hij') {
  const nodes = buttons.map(({label='',icon='',visible=true,hidden=false}) => ({
    getClientRects: () => visible ? [{}] : [],
    closest: () => hidden ? {} : null,
    getAttribute: () => label,
    querySelectorAll: () => [{textContent:icon}],
  }));
  return vm.runInNewContext(expression, {
    location:{origin,pathname}, document:{querySelectorAll:()=>nodes},
    getComputedStyle:()=>({visibility:'visible'})
  });
}
test('lobby and microphone use do not count as joined', () => {
  assert.equal(detect([{label:'Join now'},{label:'Turn off microphone'}]),false);
});
test('joined, including mute and localized hangup', () => {
  assert.equal(detect([{label:'Turn on microphone'},{icon:'call_end'}]),true);
  assert.equal(detect([{label:'Salir de la llamada'}]),true);
  assert.equal(detect([{label:'Leave call'}]),true);
});
test('hidden, detached, wrong origin and landing page do not count', () => {
  assert.equal(detect([{icon:'call_end',visible:false}]),false);
  assert.equal(detect([{icon:'call_end',hidden:true}]),false);
  assert.equal(detect([{icon:'call_end'}],'https://example.com'),false);
  assert.equal(detect([{icon:'call_end'}],'https://meet.google.com','/landing'),false);
});
