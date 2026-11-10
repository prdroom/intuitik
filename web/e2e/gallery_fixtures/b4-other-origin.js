// B-4 negative fixture: requests a page on another origin (localhost instead of
// 127.0.0.1, same port) after load.
window.addEventListener('load', () => {
  setTimeout(() => {
    window.location.href = window.location.href.replace('//127.0.0.1:', '//localhost:');
  }, 50);
});
