package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"
)

func serveQuizPage(questions []quizQuestion) (string, func(), error) {
	data, err := json.Marshal(questions)
	if err != nil {
		return "", nil, err
	}
	page := renderQuizPage(template.JSEscapeString(string(data)))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	return "http://" + listener.Addr().String(), shutdown, nil
}

func renderQuizPage(questionsJSON string) string {
	return strings.Replace(quizHTML, "__QUESTIONS__", questionsJSON, 1)
}

const quizHTML = `<!doctype html>
<html>
<head>
  <title>Wikipedia Computer Use Quiz</title>
  <style>
    body { font-family: system-ui, sans-serif; max-width: 860px; margin: 3rem auto; line-height: 1.45; }
    .choices { display: grid; gap: .5rem; margin-top: 1rem; }
    button { text-align: left; padding: .75rem 1rem; font-size: 1rem; }
    #prompt { white-space: pre-wrap; }
    #status { margin-top: 1rem; font-weight: 700; }
  </style>
</head>
<body>
  <main>
    <h1>Wikipedia Computer Use Quiz</h1>
    <p id="progress"></p>
    <p id="prompt"></p>
    <div id="choices" class="choices"></div>
    <p id="status">Choose the best answer.</p>
  </main>
  <script>
    const questions = JSON.parse('__QUESTIONS__');
    let index = 0;
    let score = 0;
    function render() {
      if (index >= questions.length) {
        document.getElementById('progress').textContent = 'Quiz complete';
        document.getElementById('prompt').textContent = 'Final score: ' + score + ' / ' + questions.length;
        document.getElementById('choices').innerHTML = '';
        document.getElementById('status').textContent = score === questions.length ? 'Perfect score.' : 'Finished.';
        return;
      }
      const q = questions[index];
      document.getElementById('progress').textContent = 'Question ' + (index + 1) + ' of ' + questions.length;
      document.getElementById('prompt').textContent = q.prompt;
      document.getElementById('status').textContent = 'Choose the best answer.';
      const choices = document.getElementById('choices');
      choices.innerHTML = '';
      q.choices.forEach(choice => {
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = choice;
        button.setAttribute('aria-label', choice);
        button.addEventListener('click', () => {
          if (choice === q.answer) {
            score++;
            document.getElementById('status').textContent = 'Correct: ' + choice;
          } else {
            document.getElementById('status').textContent = 'Incorrect: ' + choice + '. Correct answer: ' + q.answer;
          }
          index++;
          setTimeout(render, 150);
        });
        choices.appendChild(button);
      });
    }
    render();
  </script>
</body>
</html>`
