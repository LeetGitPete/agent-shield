// Patches the architecture diagram page after every Archify render.
//
// Usage: render agent-shield-architecture.html with Archify from
// agent-shield.architecture.json, then run, from any directory:
//
//   node docs/diagrams/patch-panel.mjs
//
// The script works on the page, the diagram's source and the notes file
// beside it, and takes no arguments. It:
//
// - caps the node panel at the visible height of the diagram area, with the
//   node title and the panel's buttons pinned and the rest scrolling;
// - widens the panel on desktop;
// - shows the notes in agent-shield.notes.json under the clicked node's
//   title and under each of its connections;
// - removes the template's comment line that introduces the viewer's
//   openExport auto-open code.
//
// It stops without writing the page when the notes and the diagram's source
// disagree on which nodes and connections exist, or when the page lacks the
// markup the added code hooks into. Running it again is harmless: the style
// and the script it adds sit between marker comments and are replaced.
// Only Node's built-in modules are used.

import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const pagePath = fileURLToPath(new URL('agent-shield-architecture.html', import.meta.url));
const sourcePath = fileURLToPath(new URL('agent-shield.architecture.json', import.meta.url));
const notesPath = fileURLToPath(new URL('agent-shield.notes.json', import.meta.url));

// The markup the added style and script hook into: the diagram area, the
// panel, its head, title, sublabel line, id chip, buttons and connection
// list, the attribute Archify puts each connection's id in, and the places
// the blocks go.
const hooks = [
  'class="diagram-container"',
  'id="focus-chip"',
  'class="relationship-lens-head"',
  'class="relationship-lens-copy"',
  'id="relationship-lens-title"',
  'id="focus-detail"',
  'id="focus-id"',
  'class="relationship-lens-actions"',
  'id="relationship-lens-list"',
  'data-relationship-id',
  '</head>',
  '</body>',
];

// The template's comment line, matched by its start and its end only.
const exportComment = /^[ \t]*\/\/ Auto-open on page load[^\n]*\?openExport=1[ \t]*\r?\n/gm;

function marker(name, side) {
  return `<!-- agent-shield panel patch: ${name} ${side} -->`;
}

// A block this script added on an earlier run, with its line break.
function markedBlock(name) {
  return new RegExp(`^[ \\t]*${marker(name, 'start')}\\r?\\n[\\s\\S]*?${marker(name, 'end')}\\r?\\n`, 'm');
}

function readText(path) {
  try {
    return readFileSync(path, 'utf8');
  } catch (error) {
    fail(`Cannot read ${path}: ${error.message}`);
  }
}

function readJson(path) {
  try {
    return JSON.parse(readText(path));
  } catch (error) {
    fail(`Cannot parse ${path}: ${error.message}`);
  }
}

function fail(...lines) {
  for (const line of lines) console.error(line);
  console.error('The page was not written.');
  process.exit(1);
}

function own(map, key) {
  return map !== null && typeof map === 'object' && Object.hasOwn(map, key) ? map[key] : undefined;
}

const page = readText(pagePath);
const source = readJson(sourcePath);
const notes = readJson(notesPath);

// The page without the blocks of an earlier run. They are inserted again
// below, and must not count as the markup they hook into.
const rendered = page.replace(markedBlock('style'), '').replace(markedBlock('script'), '');

const nodeIds = (source.components ?? []).map((component) => component.id);
const connections = source.connections ?? [];
const connectionIds = connections.filter((connection) => connection.id).map((connection) => connection.id);

const problems = {
  'Nodes without notes': [],
  'Connections without notes': [],
  'Node ids in the notes that the diagram does not have': [],
  'Connection ids in the notes that the diagram does not have': [],
  'Notes that are not a list of text': [],
  'Markup the patch hooks into, missing from the page': [],
};

function checkLines(kind, id, lines, missing) {
  if (lines === undefined || (Array.isArray(lines) && lines.length === 0)) {
    missing.push(id);
  } else if (!Array.isArray(lines) || !lines.every((line) => typeof line === 'string' && line.trim() !== '')) {
    problems['Notes that are not a list of text'].push(`${kind} ${id}`);
  }
}

for (const id of nodeIds) checkLines('node', id, own(notes.nodes, id), problems['Nodes without notes']);
for (const connection of connections) {
  if (connection.id) {
    checkLines('connection', connection.id, own(notes.connections, connection.id), problems['Connections without notes']);
  } else {
    problems['Connections without notes'].push(`${connection.from} -> ${connection.to} (has no id to key notes by)`);
  }
}
for (const id of Object.keys(notes.nodes ?? {})) {
  if (!nodeIds.includes(id)) problems['Node ids in the notes that the diagram does not have'].push(id);
}
for (const id of Object.keys(notes.connections ?? {})) {
  if (!connectionIds.includes(id)) problems['Connection ids in the notes that the diagram does not have'].push(id);
}
for (const hook of hooks) {
  if (!rendered.includes(hook)) problems['Markup the patch hooks into, missing from the page'].push(hook);
}

const found = Object.entries(problems).filter(([, items]) => items.length > 0);
if (found.length > 0) {
  fail(...found.flatMap(([kind, items]) => [`${kind}:`, ...items.map((item) => `  ${item}`)]));
}

// The notes in the diagram's order, so the page does not change when only
// the order of the notes file does.
const embedded = {
  nodes: Object.fromEntries(nodeIds.map((id) => [id, notes.nodes[id]])),
  connections: Object.fromEntries(connectionIds.map((id) => [id, notes.connections[id]])),
};

const style = `
    /* AgentShield panel patch, added by patch-panel.mjs after the Archify
       render. The node panel is capped at the visible part of the diagram
       area (the script below sets its max-height): the title and the buttons
       stay in the pinned head, the rest scrolls in one area. */
    #focus-chip:not([hidden]) {
      display: flex;
      flex-direction: column;
    }
    #focus-chip > .relationship-lens-head {
      position: relative;
      z-index: 1;
      flex: none;
      padding-bottom: 0;
      border-bottom: 0;
    }
    /* The buttons hang over the top of the scrolling area instead of making
       the head taller, so the sublabel stays right under the title. */
    #focus-chip .relationship-lens-actions {
      margin-bottom: -20rem;
      background: var(--toolbar-menu-bg);
    }
    #focus-chip .agent-shield-panel-scroll {
      flex: 0 1 auto;
      min-height: 0;
      overflow-y: auto;
      overscroll-behavior: contain;
      scrollbar-width: thin;
      scrollbar-color: var(--frontend-stroke) transparent;
    }
    #focus-chip .agent-shield-panel-scroll::-webkit-scrollbar { width: 4px; }
    #focus-chip .agent-shield-panel-scroll::-webkit-scrollbar-thumb {
      border-radius: 999px;
      background: color-mix(in srgb, var(--frontend-stroke) 68%, transparent);
    }
    /* The head's former lower part. Its content flows around the buttons
       and takes the full width below them; the script measures the corner
       they take. */
    #focus-chip .agent-shield-panel-body {
      display: flow-root;
      padding: 0 0.75rem 0.65rem;
      border-bottom: 1px solid var(--toolbar-border);
      background: linear-gradient(120deg, color-mix(in srgb, var(--frontend-fill) 34%, transparent), transparent 72%);
    }
    #focus-chip .agent-shield-panel-body::before {
      content: '';
      float: right;
      width: var(--agent-shield-actions-width, 4.75rem);
      height: var(--agent-shield-actions-overhang, 1.5rem);
    }
    /* The list scrolls with the rest instead of on its own. */
    #focus-chip .relationship-lens-list {
      max-height: none;
      overflow: visible;
    }
    @media (min-width: 721px) {
      #focus-chip { width: min(26rem, calc(100% - 13rem)); }
    }
    @media (max-width: 720px) {
      #focus-chip:not([data-relations-expanded="true"]) .agent-shield-panel-body { border-bottom-color: transparent; }
      /* The preview of one connection stays the compact peek card. */
      #focus-chip[data-relationship-previewing="true"] .agent-shield-connection-notes { display: none; }
    }
    /* Plain block flow, so only the lines beside the buttons are shortened. */
    #focus-chip .agent-shield-node-notes {
      display: block;
      margin: 0.55rem 0 0;
      padding: 0;
      list-style: none;
      color: var(--text-muted);
      font-size: 0.59375rem;
      line-height: 1.45;
    }
    #focus-chip .agent-shield-node-notes[hidden] { display: none; }
    #focus-chip .agent-shield-node-notes li {
      position: relative;
      padding-left: 0.62rem;
    }
    #focus-chip .agent-shield-node-notes li + li { margin-top: 0.2rem; }
    #focus-chip .agent-shield-node-notes li::before {
      content: '';
      position: absolute;
      left: 0.1rem;
      top: 0.6em;
      width: 0.2rem;
      height: 0.2rem;
      border-radius: 50%;
      background: color-mix(in srgb, var(--frontend-stroke) 70%, transparent);
    }
    #focus-chip .agent-shield-connection-notes {
      display: grid;
      grid-column: 2;
      gap: 0.1rem;
      margin-top: 0.14rem;
      color: var(--text-muted);
      font-size: 0.5625rem;
      line-height: 1.4;
      white-space: normal;
    }
`;

const notesJson = JSON.stringify(embedded, null, 2).replace(/</g, '\\u003c').replace(/\n/g, '\n      ');

const script = `
    /* AgentShield panel patch, added by patch-panel.mjs after the Archify
       render. Moves everything under the panel's title into one scrolling
       area, caps the panel at the visible part of the diagram area, and shows
       the notes for the focused node and each of its connections. */
    (function () {
      var notes = ${notesJson};
      var container = document.querySelector('.diagram-container');
      var chip = document.getElementById('focus-chip');
      var title = document.getElementById('relationship-lens-title');
      var detail = document.getElementById('focus-detail');
      var focusedId = document.getElementById('focus-id');
      var list = document.getElementById('relationship-lens-list');
      if (!container || !chip || !title || !detail || !focusedId || !list) return;
      var head = chip.querySelector('.relationship-lens-head');
      var titleColumn = chip.querySelector('.relationship-lens-copy');
      var actions = chip.querySelector('.relationship-lens-actions');
      if (!head || !titleColumn || !actions || title.parentNode !== titleColumn) return;

      var scroller = document.createElement('div');
      scroller.className = 'agent-shield-panel-scroll';
      var panelBody = document.createElement('div');
      panelBody.className = 'agent-shield-panel-body';
      while (title.nextSibling) panelBody.appendChild(title.nextSibling);
      scroller.appendChild(panelBody);
      chip.insertBefore(scroller, head.nextSibling);
      scroller.appendChild(list);

      var nodeNotes = document.createElement('ul');
      nodeNotes.className = 'agent-shield-node-notes';
      nodeNotes.hidden = true;
      detail.parentNode.insertBefore(nodeNotes, detail.nextSibling);

      function own(map, key) {
        return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : null;
      }

      function appendLines(parent, tag, lines) {
        lines.forEach(function (line) {
          var item = document.createElement(tag);
          item.textContent = line;
          parent.appendChild(item);
        });
      }

      // The corner the buttons take below the head, with the gap the title
      // keeps from them, so the scrolling area's content flows around it.
      function measureActions() {
        var actionsBox = actions.getBoundingClientRect();
        var width = actionsBox.right - titleColumn.getBoundingClientRect().right;
        var overhang = actionsBox.bottom - head.getBoundingClientRect().bottom;
        if (width <= 0) return;
        chip.style.setProperty('--agent-shield-actions-width', width + 'px');
        chip.style.setProperty('--agent-shield-actions-overhang', Math.max(overhang, 0) + 'px');
      }

      // The same visible part of the diagram area that Archify places the
      // panel in, so the capped panel always fits there. The floor keeps the
      // panel usable when the diagram area is scrolled almost out of view.
      function fitPanel() {
        if (chip.hidden) return;
        var area = container.getBoundingClientRect();
        var margin = window.innerWidth <= 720 ? 8 : 16;
        var top = Math.max(margin, margin - area.top);
        var bottom = Math.min(area.height - margin, window.innerHeight - area.top - margin);
        chip.style.maxHeight = Math.max(bottom - top, 160) + 'px';
      }

      var shownId = null;
      function sync() {
        var id = chip.hidden ? '' : focusedId.textContent;
        if (id !== shownId) {
          shownId = id;
          scroller.scrollTop = 0;
        }
        nodeNotes.textContent = '';
        appendLines(nodeNotes, 'li', own(notes.nodes, id) || []);
        nodeNotes.hidden = !nodeNotes.firstChild;
        Array.prototype.forEach.call(list.querySelectorAll('[data-relationship-id]'), function (row) {
          var connectionId = row.getAttribute('data-relationship-id');
          var lines = own(notes.connections, connectionId);
          if (!lines || row.querySelector('.agent-shield-connection-notes')) return;
          var rowNotes = document.createElement('span');
          rowNotes.className = 'agent-shield-connection-notes';
          rowNotes.id = 'agent-shield-connection-notes-' + connectionId;
          appendLines(rowNotes, 'span', lines);
          row.appendChild(rowNotes);
          row.setAttribute('aria-describedby', rowNotes.id);
        });
        if (!chip.hidden) measureActions();
        fitPanel();
      }

      // Archify rebuilds the connection list whenever the panel opens,
      // switches to another node or closes.
      var observer = new MutationObserver(sync);
      observer.observe(list, { childList: true });
      observer.observe(chip, { attributes: true, attributeFilter: ['hidden'] });
      if (typeof ResizeObserver === 'function') {
        var resizeObserver = new ResizeObserver(measureActions);
        resizeObserver.observe(titleColumn);
        resizeObserver.observe(actions);
      }
      window.addEventListener('scroll', fitPanel, { passive: true });
      window.addEventListener('resize', fitPanel);
      sync();
    })();
`;

// The blocks take the page's line breaks.
const eol = rendered.includes('\r\n') ? '\r\n' : '\n';
// name is both the marker's name and the element: style or script.
function block(name, content) {
  const text = `  ${marker(name, 'start')}\n  <${name}>${content}  </${name}>\n  ${marker(name, 'end')}\n`;
  return text.replace(/\n/g, eol);
}

function insertBefore(html, anchor, text) {
  const at = anchor === '</body>' ? html.lastIndexOf(anchor) : html.indexOf(anchor);
  return html.slice(0, at) + text + html.slice(at);
}

let patched = rendered.replace(exportComment, '');
const commentRemoved = patched !== rendered;
patched = insertBefore(patched, '</head>', block('style', style));
patched = insertBefore(patched, '</body>', block('script', script));

writeFileSync(pagePath, patched);
console.log(`Patched ${pagePath}: notes for ${nodeIds.length} nodes and ${connectionIds.length} connections.`);
console.log(commentRemoved ? 'Removed the template comment line.' : 'The template comment line was not there.');
console.log(patched === page ? 'The page was already up to date.' : 'The page changed.');
