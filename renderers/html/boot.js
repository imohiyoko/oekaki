// The bootstrap for a page whose graph is a separate document.
//
// It is a file rather than an inline script because a served page usually
// arrives with a Content-Security-Policy, and `script-src 'self'` — the
// ordinary, correct setting — blocks inline execution. An inline bootstrap
// fails there in the worst possible way: the fetch never runs, the viewer
// never loads, and the error handler that would have said so was in the
// blocked script. The page renders as an empty canvas, which reads as an
// estate with nothing in it.
//
// The urls come from data attributes rather than being written into this
// file, so it stays byte-identical for every diagram and a server can cache
// one copy of it.
(function () {
  var element = document.getElementById('oekaki-graph');
  var graph = document.body.dataset.graph;
  var app = document.body.dataset.app;

  function fail(message) {
    // Saying so beats an empty canvas that looks like an empty estate.
    document.getElementById('status').textContent = 'could not load the graph: ' + message;
  }

  if (!element || !graph || !app) {
    fail('the page does not say where its graph is');
    return;
  }

  fetch(graph).then(function (response) {
    if (!response.ok) {
      throw new Error(response.status + ' ' + response.statusText);
    }
    return response.text();
  }).then(function (text) {
    // app.js reads the graph out of this element, the same way it does in a
    // self-contained page. Filling it before loading app.js keeps one code
    // path for both kinds of page, so a bug cannot reproduce in one and not
    // the other.
    element.textContent = text;

    var script = document.createElement('script');
    script.src = app;
    script.addEventListener('error', function () {
      fail('the viewer script did not load from ' + app);
    });
    document.body.appendChild(script);
  }).catch(function (error) {
    fail(error.message);
  });
})();
