// B-3 negative fixture: the Copy handler writes the clipboard, then sets a style attribute.
const block = document.querySelector('.ds-code');
document.querySelector('button').addEventListener('click', () => {
  const done = (state) => {
    block.setAttribute('data-copy-state', state);
    block.setAttribute('style', 'outline: 1px solid');
  };
  navigator.clipboard.writeText(block.querySelector('code').textContent).then(() => done('copied'), () => done('failed'));
});
