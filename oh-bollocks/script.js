'use strict';

const advice = {
  overwhelmed: 'Pick one small thing, love. Not seventeen. Do that, have a breather, and remember: you’re a person, not a productivity app.',
  dating: 'Put the conversational wheelbarrow down. Ask a real question, give them room to answer, and notice whether they’re curious about you too.',
  plans: 'Be kind, give them notice, and skip the elaborate excuse. “I need a quiet evening, but thank you for inviting me” is perfectly respectable.',
  comparison: 'You’re comparing your kitchen floor to someone else’s shop window. Step away from the scrolling. Nobody posts the laundry chair.'
};

const dilemma = document.querySelector('#dilemma');
const answer = document.querySelector('#advice-answer');
document.querySelector('#advice-button').addEventListener('click', () => {
  answer.textContent = advice[dilemma.value];
});

const quotes = [
  '“You don’t have to rise and shine. You can just rise and put the kettle on.”',
  '“A dressing gown is just a power suit with better priorities.”',
  '“Some days you seize the day. Some days you find the clean mugs. Both count.”',
  '“Protect your peace. And the good biscuits.”',
  '“Not every thought needs a meeting. Especially before coffee.”'
];
let quoteIndex = 0;
document.querySelector('#quote-button').addEventListener('click', () => {
  quoteIndex = (quoteIndex + 1) % quotes.length;
  document.querySelector('#quote').textContent = quotes[quoteIndex];
});
