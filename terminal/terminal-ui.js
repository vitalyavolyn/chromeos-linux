(function () {
  var d = document;
  var pid = null;
  var dec = new TextDecoder();
  var out = null;
  var lines = [[]];
  var row = 0;
  var col = 0;
  var currentStyle = "";
  var savedRow = 0;
  var savedCol = 0;

  var fgColors = [
    "#000000",
    "#cd3131",
    "#0dbc79",
    "#e5e510",
    "#2472c8",
    "#bc3fbc",
    "#11a8cd",
    "#e5e5e5"
  ];
  var brightFgColors = [
    "#666666",
    "#f14c4c",
    "#23d18b",
    "#f5f543",
    "#3b8eea",
    "#d670d6",
    "#29b8db",
    "#ffffff"
  ];
  var bgColors = [
    "#000000",
    "#cd3131",
    "#0dbc79",
    "#e5e510",
    "#2472c8",
    "#bc3fbc",
    "#11a8cd",
    "#e5e5e5"
  ];

  function ensureRow() {
    while (lines.length <= row) lines.push([]);
  }

  function blankCell() {
    return { ch: " ", style: "" };
  }

  function escapeHtml(text) {
    return text
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }

  function putChar(ch) {
    ensureRow();
    var line = lines[row];
    while (line.length < col) line.push(blankCell());
    line[col] = { ch: ch, style: currentStyle };
    col++;
  }

  function render() {
    if (!out) return;
    var html = lines.map(function (line) {
      var chunks = [];
      var style = null;
      var text = "";
      function flush() {
        if (!text) return;
        chunks.push(style
          ? "<span style=\"" + style + "\">" + escapeHtml(text) + "</span>"
          : escapeHtml(text));
        text = "";
      }
      line.forEach(function (cell) {
        var cellStyle = cell.style || "";
        if (cellStyle !== style) {
          flush();
          style = cellStyle;
        }
        text += cell.ch;
      });
      flush();
      return chunks.join("");
    }).join("\n");
    out.innerHTML = html;
    out.scrollTop = out.scrollHeight;
  }

  function setSgr(params) {
    if (!params.length) params = [0];
    var styles = {};
    currentStyle.split(";").forEach(function (part) {
      var kv = part.split(":");
      if (kv.length === 2) styles[kv[0].trim()] = kv[1].trim();
    });

    params.forEach(function (p) {
      if (p === 0) styles = {};
      else if (p === 1) styles["font-weight"] = "700";
      else if (p === 3) styles["font-style"] = "italic";
      else if (p === 4) styles["text-decoration"] = "underline";
      else if (p === 22) delete styles["font-weight"];
      else if (p === 23) delete styles["font-style"];
      else if (p === 24) delete styles["text-decoration"];
      else if (p === 39) delete styles.color;
      else if (p === 49) delete styles["background-color"];
      else if (p >= 30 && p <= 37) styles.color = fgColors[p - 30];
      else if (p >= 90 && p <= 97) styles.color = brightFgColors[p - 90];
      else if (p >= 40 && p <= 47) styles["background-color"] = bgColors[p - 40];
      else if (p >= 100 && p <= 107) styles["background-color"] = brightFgColors[p - 100];
    });

    currentStyle = Object.keys(styles).map(function (key) {
      return key + ":" + styles[key];
    }).join(";");
  }

  function processCsi(cmd, params) {
    var n = params[0] || 1;
    if (cmd === "A") row = Math.max(0, row - n);
    else if (cmd === "B") {
      row += n;
      ensureRow();
    } else if (cmd === "C") col += n;
    else if (cmd === "D") col = Math.max(0, col - n);
    else if (cmd === "G") col = Math.max(0, n - 1);
    else if (cmd === "s") {
      savedRow = row;
      savedCol = col;
    } else if (cmd === "u") {
      row = savedRow;
      col = savedCol;
      ensureRow();
    } else if (cmd === "m") {
      setSgr(params);
    }
    else if (cmd === "H" || cmd === "f") {
      row = Math.max(0, (params[0] || 1) - 1);
      col = Math.max(0, (params[1] || 1) - 1);
      ensureRow();
    } else if (cmd === "K") {
      ensureRow();
      if (!params[0]) lines[row] = lines[row].slice(0, col);
      else if (params[0] === 1) {
        while (lines[row].length < col) lines[row].push(blankCell());
        for (var i = 0; i < col; i++) lines[row][i] = blankCell();
      } else if (params[0] === 2) lines[row] = [];
    } else if (cmd === "J") {
      ensureRow();
      if (!params[0]) {
        lines[row] = lines[row].slice(0, col);
        lines.splice(row + 1);
      } else if (params[0] === 1) {
        lines.splice(0, row);
        row = 0;
        col = 0;
      } else if (params[0] === 2 || params[0] === 3) {
        lines = [[]];
        row = 0;
        col = 0;
      }
    }
  }

  function processText(str) {
    var i = 0;
    while (i < str.length) {
      var c = str[i];
      if (c === "\x1b") {
        if (str[i + 1] === "[") {
          var j = i + 2;
          while (j < str.length && !/[A-Za-z]/.test(str[j])) j++;
          if (j < str.length) {
            var params = str
              .slice(i + 2, j)
              .replace(/[^0-9;]/g, "")
              .split(";")
              .map(function (x) { return parseInt(x, 10) || 0; });
            processCsi(str[j], params);
            i = j + 1;
          } else {
            i = str.length;
          }
        } else if (str[i + 1] === "]") {
          var k = i + 2;
          while (k < str.length && str[k] !== "\x07" && !(str[k] === "\x1b" && str[k + 1] === "\\")) k++;
          i = str[k] === "\x07" ? k + 1 : str[k] === "\x1b" ? k + 2 : str.length;
        } else {
          i += 2;
        }
      } else if (c === "\r") {
        col = 0;
        i++;
      } else if (c === "\n") {
        row++;
        ensureRow();
        i++;
      } else if (c === "\x08") {
        col = Math.max(0, col - 1);
        i++;
      } else if (c < " " && c !== "\t") {
        i++;
      } else {
        putChar(c);
        i++;
      }
    }
  }

  function write(data) {
    var str = data instanceof ArrayBuffer
      ? dec.decode(new Uint8Array(data))
      : data instanceof Uint8Array
        ? dec.decode(data)
        : typeof data === "string"
          ? data
          : String(data || "");
    processText(str);
    render();
  }

  d.head.insertAdjacentHTML(
    "beforeend",
    "<style>*{box-sizing:border-box}html,body{margin:0;padding:0;height:100%;background:#1e1e1e;color:#d4d4d4;font:13px/1.5 monospace;overflow:hidden}#out{height:calc(100% - 28px);overflow-y:auto;overflow-x:auto;padding:6px 8px;white-space:pre}#inp{width:100%;height:28px;background:#252526;color:#d4d4d4;font:13px monospace;border:none;border-top:1px solid #444;outline:none;padding:4px 8px}</style>"
  );

  function start() {
    d.body.innerHTML = "<div id=out></div><input id=inp autofocus>";
    out = d.getElementById("out");
    var input = d.getElementById("inp");
    lines = [[]];
    row = 0;
    col = 0;

    d.addEventListener("click", function () { input.focus(); });
    chrome.terminalPrivate.openVmshellProcess([], function (id) {
      if (id == null) {
        write("[failed]\n");
        return;
      }
      pid = id;
      chrome.terminalPrivate.onProcessOutput.addListener(function (p, type, text) {
        if (p !== pid) return;
        if (type === "exit") write("\n[exit]\n");
        else write(text);
      });
      input.focus();
    });

    input.addEventListener("keydown", function (e) {
      if (pid == null) return;
      var text = "";
      if (e.key === "Enter") text = "\r";
      else if (e.key === "Backspace") text = "\x7f";
      else if (e.key === "Tab") {
        e.preventDefault();
        text = "\t";
      } else if (e.ctrlKey && e.key.length === 1) {
        text = String.fromCharCode(e.key.toUpperCase().charCodeAt(0) - 64);
        e.preventDefault();
      } else if (!e.ctrlKey && !e.altKey && !e.metaKey && e.key.length === 1) {
        text = e.key;
      }
      if (text) chrome.terminalPrivate.sendInput(pid, text, function () {});
    });
  }

  if (d.readyState === "loading") d.addEventListener("DOMContentLoaded", start);
  else start();
}());
