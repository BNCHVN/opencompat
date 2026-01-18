// RT-math - main.js
// Yêu cầu: ngẫu nhiên không lặp, phép + − × ÷, số 1-100, 15s/câu mặc định

// DOM
const questionEl = document.getElementById('question');
const answerInput = document.getElementById('answerInput');
const submitBtn = document.getElementById('submitBtn');
const skipBtn = document.getElementById('skipBtn');
const startBtn = document.getElementById('startBtn');
const restartBtn = document.getElementById('restartBtn');

const timerEl = document.getElementById('timer');
const totalTimeEl = document.getElementById('totalTime');
const avgTimeEl = document.getElementById('avgTime');
const scoreEl = document.getElementById('score');
const progressFill = document.getElementById('progressFill');
const messageEl = document.getElementById('message');

const timePerQSelect = document.getElementById('timePerQ');
const targetScoreInput = document.getElementById('targetScore');

// Game settings / state
let timePerQuestion = parseFloat(timePerQSelect.value) || 15;
let targetScore = parseInt(targetScoreInput.value, 10) || 100;

let generatedQuestions = new Set();

let timerInterval = null;
let timeRemaining = 0;
let questionStartTs = 0;
let totalTimeUsed = 0;
let questionsAnswered = 0;
let score = 0;
let running = false;

let currentAnswer = null;
let currentKey = null;

// Utility
function randInt(min, max){ return Math.floor(Math.random()*(max-min+1))+min; }

// Generate question: operands in [1,100], operators + - * /, division yields integer result, no repeats
function generateQuestion() {
  const operators = ['+', '-', '*', '/'];
  const maxRange = 100;
  let a, b, op, displayOp, answer, key;
  let attempts = 0;
  do {
    attempts++;
    op = operators[Math.floor(Math.random() * operators.length)];
    if (op === '+') {
      a = randInt(1, maxRange);
      b = randInt(1, maxRange);
      answer = a + b;
      displayOp = '+';
    } else if (op === '-') {
      a = randInt(1, maxRange);
      b = randInt(1, maxRange);
      if (a < b) [a, b] = [b, a];
      answer = a - b;
      displayOp = '−';
    } else if (op === '*') {
      a = randInt(1, maxRange);
      b = randInt(1, maxRange);
      answer = a * b;
      displayOp = '×';
    } else { // '/'
      // choose integer result r and divisor b so that a = r*b <= 100 and both a,b in [1,100]
      // pick r from 1..100, then pick b from 1..floor(100/r)
      const r = randInt(1, 100);
      const maxB = Math.floor(100 / r) || 1;
      b = randInt(1, maxB);
      a = r * b; // a <=100
      answer = r;
      displayOp = '÷';
    }
    key = `${a}${displayOp}${b}`;
    // safety: break if too many attempts (shouldn't happen)
    if (attempts > 1000) break;
  } while (generatedQuestions.has(key) && generatedQuestions.size < 10000);

  generatedQuestions.add(key);
  return { text: `${a} ${displayOp} ${b} = ?`, answer, key };
}

// Timer
function startTimer(){
  clearInterval(timerInterval);
  timeRemaining = timePerQuestion;
  questionStartTs = performance.now();
  updateTimerDisplay();
  timerInterval = setInterval(() => {
    timeRemaining -= 0.1;
    if (timeRemaining <= 0){
      timeRemaining = 0;
      updateTimerDisplay();
      clearInterval(timerInterval);
      handleTimeout();
    } else updateTimerDisplay();
  }, 100);
}
function updateTimerDisplay(){ timerEl.textContent = `${timeRemaining.toFixed(1)}s`; }

// UI updates
function updateUI(){
  scoreEl.textContent = String(score);
  totalTimeEl.textContent = `${totalTimeUsed.toFixed(2)}s`;
  avgTimeEl.textContent = questionsAnswered ? `${(totalTimeUsed / questionsAnswered).toFixed(2)}s` : '0.00s';
  const percent = Math.min(100, Math.round((score / targetScore) * 100));
  progressFill.style.width = `${percent}%`;
  progressFill.setAttribute('aria-valuenow', percent);
}

// Flow
function nextQuestion(){
  const q = generateQuestion();
  currentAnswer = q.answer;
  currentKey = q.key;
  questionEl.textContent = q.text;
  answerInput.value = '';
  answerInput.focus();
  messageEl.classList.add('hidden');
  startTimer();
}

function handleSubmit(){
  if (!running) return;
  const raw = answerInput.value.trim();
  if (raw === '') {
    showMessage('Bạn chưa nhập đáp án', 'wrong');
    return;
  }
  const given = Number(raw);
  const timeTaken = (performance.now() - questionStartTs) / 1000;
  totalTimeUsed += timeTaken;
  questionsAnswered += 1;

  if (Number.isFinite(given) && Math.abs(given - currentAnswer) < 1e-9){
    const base = 10;
    const speedBonus = Math.max(0, Math.round((timePerQuestion - timeTaken) * 2));
    const gained = base + speedBonus;
    score += gained;
    showMessage(`Đúng! +${gained} điểm (tốc độ ${timeTaken.toFixed(2)}s)`, 'correct');
  } else {
    const penalty = 5;
    score = Math.max(0, score - penalty);
    showMessage(`Sai. Đáp án: ${currentAnswer}. -${penalty} điểm`, 'wrong');
  }

  updateUI();
  clearInterval(timerInterval);

  setTimeout(() => {
    if (score >= targetScore) {
      endGame(true);
    } else {
      nextQuestion();
    }
  }, 700);
}

function handleSkip(){
  if (!running) return;
  const timeTaken = timePerQuestion;
  totalTimeUsed += timeTaken;
  questionsAnswered += 1;
  const penalty = 4;
  score = Math.max(0, score - penalty);
  showMessage(`Bỏ qua. Đáp án: ${currentAnswer}. -${penalty} điểm`, 'wrong');
  updateUI();
  clearInterval(timerInterval);
  setTimeout(() => {
    if (score >= targetScore) endGame(true);
    else nextQuestion();
  }, 600);
}

function handleTimeout(){
  if (!running) return;
  questionsAnswered += 1;
  totalTimeUsed += timePerQuestion;
  const penalty = 6;
  score = Math.max(0, score - penalty);
  showMessage(`Hết giờ! Đáp án: ${currentAnswer}. -${penalty} điểm`, 'wrong');
  updateUI();
  setTimeout(() => {
    if (score >= targetScore) endGame(true);
    else nextQuestion();
  }, 800);
}

function showMessage(txt, type){
  messageEl.textContent = txt;
  messageEl.className = 'panel';
  if (type === 'correct') messageEl.classList.add('correct');
  else if (type === 'wrong') messageEl.classList.add('wrong');
  else messageEl.classList.add('');
}

function endGame(won){
  running = false;
  clearInterval(timerInterval);
  if (won) showMessage(`Chúc mừng! Bạn đạt mục tiêu ${score} điểm. Tổng thời gian: ${totalTimeUsed.toFixed(2)}s`, 'correct');
  else showMessage(`Kết thúc. Điểm: ${score}. Tổng thời gian: ${totalTimeUsed.toFixed(2)}s`, 'wrong');
}

// Start/restart
function startGame(){
  // đọc cấu hình
  timePerQuestion = parseFloat(timePerQSelect.value) || 15;
  targetScore = Math.max(10, parseInt(targetScoreInput.value, 10) || 100);

  // reset state
  generatedQuestions.clear();
  totalTimeUsed = 0;
  questionsAnswered = 0;
  score = 0;
  running = true;
  updateUI();
  nextQuestion();
}

function restartGame(){
  clearInterval(timerInterval);
  running = false;
  generatedQuestions.clear();
  totalTimeUsed = 0;
  questionsAnswered = 0;
  score = 0;
  updateUI();
  questionEl.textContent = 'Nhấn Bắt đầu để chơi';
  timerEl.textContent = `${timePerQuestion.toFixed(1)}s`;
  messageEl.classList.add('hidden');
}

// Events
submitBtn.addEventListener('click', handleSubmit);
skipBtn.addEventListener('click', handleSkip);
startBtn.addEventListener('click', startGame);
restartBtn.addEventListener('click', () => { restartGame(); startGame(); });

document.addEventListener('keydown', (e) => {
  if (e.code === 'Enter') {
    if (!running) { startGame(); return; }
    handleSubmit();
  }
  if (e.code === 'Space') { e.preventDefault(); handleSkip(); }
});

timePerQSelect.addEventListener('change', () => {
  timePerQuestion = parseFloat(timePerQSelect.value);
  timerEl.textContent = `${timePerQuestion.toFixed(1)}s`;
});
targetScoreInput.addEventListener('change', () => { targetScore = parseInt(targetScoreInput.value, 10) || 100; });

answerInput.addEventListener('focus', () => answerInput.select());
window.addEventListener('load', () => {
  answerInput.focus();
  timerEl.textContent = `${timePerQuestion.toFixed(1)}s`;
});