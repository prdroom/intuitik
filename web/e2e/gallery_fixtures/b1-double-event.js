// B-1 negative fixture: one click emits the event twice.
const log = document.querySelector('[data-event-log="NEGATIVE"]');
document.querySelector('button').addEventListener('click', () => {
  for (let i = 0; i < 2; i += 1) {
    const li = document.createElement('li');
    li.textContent = 'click';
    log.appendChild(li);
  }
});
