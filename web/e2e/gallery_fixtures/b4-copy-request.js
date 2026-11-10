// B-4 negative fixture: the Copy handler writes the clipboard, then requests a page on
// another origin (localhost instead of 127.0.0.1, same port).
const block = document.querySelector('.ds-code');
document.querySelector('button').addEventListener('click', () => {
  const done = (state) => {
    block.setAttribute('data-copy-state', state);
    setTimeout(() => {
      window.location.href = window.location.href.replace('//127.0.0.1:', '//localhost:');
    }, 1000);
  };
  navigator.clipboard.writeText(block.querySelector('code').textContent).then(() => done('copied'), () => done('failed'));
});
