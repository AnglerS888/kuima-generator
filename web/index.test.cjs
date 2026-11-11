const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

test('changing color refreshes both previews after updating their formats', () => {
  const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
  const match = html.match(/colorSel\.addEventListener\('change', \(\) => \{([\s\S]*?)\n  \}\);/);
  assert.ok(match, 'color change handler exists');
  const calls = [];
  const context = {
    colorSel: { value: 'solid' }, fmt: 'png', addFmt: 'png',
    setColorMode: value => calls.push(['color', value]),
    setFormat: value => calls.push(['main format', value]),
    addSetFmt: value => calls.push(['friend format', value]),
    saveInk: () => calls.push(['save']),
    generate: () => calls.push(['main preview']),
    addGenerate: () => calls.push(['friend preview']),
  };
  vm.runInNewContext(match[1], context);
  assert.deepEqual(calls, [
    ['color', 'solid'], ['main format', 'png'], ['friend format', 'png'],
    ['save'], ['main preview'], ['friend preview'],
  ]);
});
