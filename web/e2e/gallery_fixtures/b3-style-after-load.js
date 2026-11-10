// B-3 negative fixture: sets a style attribute after the load event.
window.addEventListener('load', () => {
  setTimeout(() => {
    document.getElementById('target').setAttribute('style', 'font-weight: 700');
  }, 50);
});
