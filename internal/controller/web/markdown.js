'use strict';
// Tiny, injection-safe Markdown renderer used by the controller UI and the
// chat app for instruction previews. It never touches innerHTML: every text
// node is set via textContent, and only http(s)/mailto links are emitted.
(function () {
  const linkPattern = /^https?:\/\/[^\s<>"'`]+$/i;
  function inline(text, out) {
    let rest = text;
    const pushText = (value) => out.append(document.createTextNode(value));
    const pushSpan = (className, text) => {
      const span = document.createElement('span');
      span.className = className;
      span.textContent = text;
      out.append(span);
    };
    while (rest) {
      let m = /^`([^`]+)`/.exec(rest);
      if (m) {
        const code = document.createElement('code');
        code.textContent = m[1];
        out.append(code);
        rest = rest.slice(m[0].length);
        continue;
      }
      m = /^!?\[([^\]]*)\]\(([^)\s]+)\)/.exec(rest);
      if (m && (linkPattern.test(m[2]) || /^mailto:[^@\s]+@[^@\s]+$/.test(m[2]))) {
        const a = document.createElement('a');
        a.href = m[2];
        a.textContent = m[1] || m[2];
        a.target = '_blank';
        a.rel = 'noopener noreferrer';
        out.append(a);
        rest = rest.slice(m[0].length);
        continue;
      }
      m = /^\*\*([^*]+)\*\*/.exec(rest);
      if (m) {
        const strong = document.createElement('strong');
        strong.textContent = m[1];
        out.append(strong);
        rest = rest.slice(m[0].length);
        continue;
      }
      m = /^\*([^*]+)\*/.exec(rest);
      if (m) {
        const em = document.createElement('em');
        em.textContent = m[1];
        out.append(em);
        rest = rest.slice(m[0].length);
        continue;
      }
      const next = Math.min(...['`', '**', '*', '[', '!['].map((s) => {
        const i = rest.indexOf(s, 1);
        return i === -1 ? rest.length : i;
      }));
      if (next === Infinity || next === rest.length) {
        pushText(rest);
        break;
      }
      pushText(rest.slice(0, Math.max(next, 1)));
      rest = rest.slice(Math.max(next, 1));
    }
  }
  function block(out, lines, i) {
    const line = lines[i];
    const heading = /^(#{1,3})\s+(.+)$/.exec(line);
    if (heading) {
      const el = document.createElement('h' + (heading[1].length));
      el.className = 'md-h' + heading[1].length;
      inline(heading[2], el);
      return [el, i + 1];
    }
    if (/^\s*(```|~~~)/.test(line)) {
      const fence = line.trim()[0];
      let j = i + 1;
      const body = [];
      while (j < lines.length && !new RegExp('^\\s*' + fence + fence + fence).test(lines[j])) body.push(lines[j++]);
      const pre = document.createElement('pre');
      pre.className = 'md-pre';
      pre.textContent = body.join('\n') + '\n';
      return [pre, Math.min(j + 1, lines.length)];
    }
    if (/^\s*>/.test(line)) {
      const quote = document.createElement('blockquote');
      quote.className = 'md-quote';
      let j = i;
      const frag = document.createDocumentFragment();
      while (j < lines.length && /^\s*>/.test(lines[j])) {
        inline(lines[j].replace(/^\s*> ?/, ''), frag);
        frag.append(document.createTextNode('\n'));
        j++;
      }
      quote.append(frag);
      return [quote, j];
    }
    if (/^\s*([-*+]|\d+[.)])\s+/.test(line)) {
      const ordered = /^\s*\d+[.)]/.test(line);
      const list = document.createElement(ordered ? 'ol' : 'ul');
      let j = i;
      while (j < lines.length && /^\s*([-*+]|\d+[.)])\s+/.test(lines[j])) {
        const item = document.createElement('li');
        inline(lines[j].replace(/^\s*([-*+]|\d+[.)])\s+/, ''), item);
        list.append(item);
        j++;
      }
      return [list, j];
    }
    if (/^\s*---+|===+$/.test(line)) {
      const hr = document.createElement('hr');
      hr.className = 'md-hr';
      return [hr, i + 1];
    }
    const frag = document.createDocumentFragment();
    let j = i;
    while (j < lines.length && lines[j].trim() && !/^\s*(```|~~~|>|#{1,3}\s|([-*+]|\d+[.)])\s+|---+$)/.test(lines[j])) {
      inline(lines[j], frag);
      frag.append(document.createTextNode('\n'));
      j++;
      if (j >= lines.length || !lines[j].trim()) break;
    }
    const p = document.createElement('p');
    p.className = 'md-p';
    p.append(frag);
    return [p, Math.max(j, i + 1)];
  }
  // markdownToNodes renders instructions.toml content into raw DOM nodes using
  // textContent everywhere and only http(s)/mailto links — never innerHTML.
  window.markdownToNodes = function (source) {
    const fragment = document.createDocumentFragment();
    if (!source || !source.trim()) {
      const empty = document.createElement('p');
      empty.className = 'md-p md-empty';
      empty.textContent = 'No content.';
      return empty;
    }
    const lines = source.replace(/\r\n?/g, '\n').split('\n');
    let i = 0;
    while (i < lines.length) {
      if (!lines[i].trim()) {
        i++;
        continue;
      }
      const rendered = block(fragment, lines, i);
      i = rendered[1];
    }
    const root = document.createElement('div');
    root.className = 'md-root';
    root.append(fragment);
    return root;
  };
})();
