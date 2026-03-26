// ── Types ──────────────────────────────────────────────

interface Contact {
  id: number;
  name: string;
  email: string;
  phone: string;
  created_at: string;
}

interface AppState {
  contacts: Contact[];
  counter: number;
  editing: number | null;
  adding: boolean;
  loading: boolean;
}

// ── State ──────────────────────────────────────────────

const state: AppState = {
  contacts: [],
  counter: 0,
  editing: null,
  adding: false,
  loading: true,
};

// ── API ────────────────────────────────────────────────

async function fetchContacts(): Promise<Contact[]> {
  const res = await fetch('/api/contacts');
  return res.json();
}

async function createContact(data: Partial<Contact>): Promise<Contact> {
  const res = await fetch('/api/contacts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  return res.json();
}

async function updateContact(id: number, data: Partial<Contact>): Promise<Contact> {
  const res = await fetch(`/api/contacts/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  return res.json();
}

async function deleteContact(id: number): Promise<void> {
  await fetch(`/api/contacts/${id}`, { method: 'DELETE' });
}

// ── Mutations ──────────────────────────────────────────

function incrementCounter(): void {
  state.counter++;
  render();
}

function decrementCounter(): void {
  state.counter--;
  render();
}

async function handleAddContact(): Promise<void> {
  const nameEl = document.getElementById('add-name') as HTMLInputElement | null;
  const emailEl = document.getElementById('add-email') as HTMLInputElement | null;
  const phoneEl = document.getElementById('add-phone') as HTMLInputElement | null;
  const name = nameEl?.value.trim() || '';
  const email = emailEl?.value.trim() || '';
  const phone = phoneEl?.value.trim() || '';
  if (!name) return;
  const contact = await createContact({ name, email, phone });
  state.contacts.push(contact);
  state.contacts.sort((a, b) => a.name.localeCompare(b.name));
  state.adding = false;
  render();
}

async function handleSaveEdit(id: number): Promise<void> {
  const nameEl = document.getElementById('edit-name') as HTMLInputElement | null;
  const emailEl = document.getElementById('edit-email') as HTMLInputElement | null;
  const phoneEl = document.getElementById('edit-phone') as HTMLInputElement | null;
  const name = nameEl?.value.trim() || '';
  const email = emailEl?.value.trim() || '';
  const phone = phoneEl?.value.trim() || '';
  if (!name) return;
  const updated = await updateContact(id, { name, email, phone });
  const idx = state.contacts.findIndex((c) => c.id === id);
  if (idx !== -1) state.contacts[idx] = updated;
  state.contacts.sort((a, b) => a.name.localeCompare(b.name));
  state.editing = null;
  render();
}

async function handleDelete(id: number): Promise<void> {
  if (!window.confirm('Delete this contact?')) return;
  await deleteContact(id);
  state.contacts = state.contacts.filter((c) => c.id !== id);
  render();
}

// ── Rendering ──────────────────────────────────────────

function formatTime(): string {
  return new Date().toLocaleTimeString();
}

const inputStyle = `
  padding:8px 10px;border-radius:6px;border:1px solid rgba(255,255,255,0.15);
  background:rgba(255,255,255,0.06);color:#d8f3dc;font-family:inherit;font-size:0.85rem;
  outline:none;width:100%;box-sizing:border-box;
`.trim();

const btnStyle = `
  padding:6px 14px;border-radius:6px;border:none;
  background:#52b788;color:#081c15;font-weight:600;cursor:pointer;font-family:inherit;font-size:0.85rem;
`.trim();

const btnDangerStyle = `
  padding:6px 14px;border-radius:6px;border:1px solid rgba(255,255,255,0.15);
  background:none;color:#ff6b6b;cursor:pointer;font-family:inherit;font-size:0.85rem;
`.trim();

const btnGhostStyle = `
  padding:6px 14px;border-radius:6px;border:1px solid rgba(255,255,255,0.15);
  background:rgba(255,255,255,0.06);color:#d8f3dc;cursor:pointer;font-family:inherit;font-size:0.85rem;
`.trim();

function addFormHTML(): string {
  if (!state.adding) return '';
  return `
    <div style="display:grid;grid-template-columns:1fr 1fr 1fr auto auto;gap:8px;align-items:center;padding:10px 12px;background:rgba(82,183,136,0.1);border:1px solid rgba(82,183,136,0.25);border-radius:8px;margin-bottom:12px;">
      <input id="add-name" type="text" placeholder="Name *" style="${inputStyle}" />
      <input id="add-email" type="text" placeholder="Email" style="${inputStyle}" />
      <input id="add-phone" type="text" placeholder="Phone" style="${inputStyle}" />
      <button id="save-add" style="${btnStyle}">Save</button>
      <button id="cancel-add" style="${btnGhostStyle}">Cancel</button>
    </div>
  `;
}

function contactRowHTML(c: Contact): string {
  if (state.editing === c.id) {
    return `
      <div style="display:grid;grid-template-columns:1fr 1fr 1fr auto auto;gap:8px;align-items:center;padding:10px 12px;background:rgba(82,183,136,0.1);border:1px solid rgba(82,183,136,0.25);border-radius:8px;margin-bottom:6px;">
        <input id="edit-name" type="text" value="${c.name}" style="${inputStyle}" />
        <input id="edit-email" type="text" value="${c.email}" style="${inputStyle}" />
        <input id="edit-phone" type="text" value="${c.phone}" style="${inputStyle}" />
        <button data-save-edit="${c.id}" style="${btnStyle}">Save</button>
        <button data-cancel-edit style="${btnGhostStyle}">Cancel</button>
      </div>
    `;
  }
  return `
    <div style="display:grid;grid-template-columns:1fr 1fr 1fr auto auto;gap:8px;align-items:center;padding:10px 12px;background:rgba(255,255,255,0.04);border-radius:8px;margin-bottom:6px;">
      <span style="font-weight:500;color:#b7e4c7;">${c.name}</span>
      <span style="opacity:0.7;font-size:0.85rem;">${c.email || '—'}</span>
      <span style="opacity:0.7;font-size:0.85rem;">${c.phone || '—'}</span>
      <button data-edit="${c.id}" style="${btnGhostStyle}">Edit</button>
      <button data-delete="${c.id}" style="${btnDangerStyle}">&times;</button>
    </div>
  `;
}

function contactsListHTML(): string {
  if (state.loading) {
    return '<p style="color:#6bc78b;opacity:0.6;font-style:italic;margin:0;">Loading contacts&hellip;</p>';
  }
  if (state.contacts.length === 0) {
    return '<p style="color:#6bc78b;opacity:0.6;font-style:italic;margin:0;">No contacts yet &mdash; add one above</p>';
  }
  return state.contacts.map(contactRowHTML).join('');
}

function render(): void {
  const app = document.getElementById('app');
  if (!app) return;

  const port = window.location.port || '—';

  app.innerHTML = `
    <div style="
      min-height:100vh;
      background:linear-gradient(135deg, #0a2e1a 0%, #2d6a4f 100%);
      color:#d8f3dc;
      font-family:'SF Mono','Fira Code','Cascadia Code',monospace;
      display:flex;
      flex-direction:column;
      align-items:center;
      padding:60px 20px 40px;
    ">
      <h1 style="
        font-size:2.4rem;
        font-weight:700;
        margin:0 0 8px;
        color:#b7e4c7;
        letter-spacing:-0.5px;
      ">Vite + TypeScript</h1>

      <p style="margin:0 0 32px;opacity:0.6;font-size:0.9rem;">
        Port <strong style="color:#52b788;">${port}</strong>
        &nbsp;&middot;&nbsp;
        <span id="clock">${formatTime()}</span>
      </p>

      <!-- Counter -->
      <div style="
        background:rgba(0,0,0,0.25);
        border:1px solid rgba(255,255,255,0.08);
        border-radius:12px;
        padding:24px 32px;
        margin-bottom:24px;
        text-align:center;
        min-width:300px;
      ">
        <h2 style="margin:0 0 16px;font-size:1.1rem;color:#95d5b2;">Counter</h2>
        <div style="display:flex;align-items:center;justify-content:center;gap:16px;">
          <button id="dec" style="
            width:40px;height:40px;border-radius:8px;border:1px solid rgba(255,255,255,0.15);
            background:rgba(255,255,255,0.06);color:#d8f3dc;font-size:1.4rem;cursor:pointer;
          ">&minus;</button>
          <span style="font-size:2rem;font-weight:700;min-width:60px;color:#52b788;">${state.counter}</span>
          <button id="inc" style="
            width:40px;height:40px;border-radius:8px;border:1px solid rgba(255,255,255,0.15);
            background:rgba(255,255,255,0.06);color:#d8f3dc;font-size:1.4rem;cursor:pointer;
          ">+</button>
        </div>
      </div>

      <!-- Contacts -->
      <div style="
        background:rgba(0,0,0,0.25);
        border:1px solid rgba(255,255,255,0.08);
        border-radius:12px;
        padding:24px 32px;
        margin-bottom:24px;
        min-width:300px;
        max-width:700px;
        width:100%;
      ">
        <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:4px;">
          <h2 style="margin:0;font-size:1.1rem;color:#95d5b2;">Contacts</h2>
          <button id="add-btn" style="${btnStyle}${state.adding ? 'opacity:0.4;pointer-events:none;' : ''}">+ Add</button>
        </div>
        <p style="margin:0 0 16px;font-size:0.8rem;opacity:0.4;">
          ${state.contacts.length} contact${state.contacts.length !== 1 ? 's' : ''} &middot; SQLite-backed
        </p>

        ${addFormHTML()}

        <div style="display:grid;grid-template-columns:1fr 1fr 1fr auto auto;gap:8px;padding:0 12px 8px;font-size:0.75rem;opacity:0.4;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;">
          <span>Name</span><span>Email</span><span>Phone</span><span></span><span></span>
        </div>

        <div id="contacts-list">${contactsListHTML()}</div>
      </div>

      <p style="
        margin-top:auto;
        padding-top:40px;
        font-size:0.8rem;
        opacity:0.4;
        text-align:center;
        max-width:400px;
        line-height:1.5;
      ">
        If mdp is working, you should see a floating switcher widget at the top of this page.
      </p>
    </div>
  `;

  // ── Event listeners ──

  document.getElementById('inc')?.addEventListener('click', incrementCounter);
  document.getElementById('dec')?.addEventListener('click', decrementCounter);

  document.getElementById('add-btn')?.addEventListener('click', () => {
    state.adding = true;
    state.editing = null;
    render();
    document.getElementById('add-name')?.focus();
  });

  document.getElementById('save-add')?.addEventListener('click', () => void handleAddContact());
  document.getElementById('cancel-add')?.addEventListener('click', () => {
    state.adding = false;
    render();
  });

  document.getElementById('add-name')?.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Enter') void handleAddContact();
    if (e.key === 'Escape') { state.adding = false; render(); }
  });
  document.getElementById('add-email')?.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Enter') void handleAddContact();
    if (e.key === 'Escape') { state.adding = false; render(); }
  });
  document.getElementById('add-phone')?.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Enter') void handleAddContact();
    if (e.key === 'Escape') { state.adding = false; render(); }
  });

  const contactsList = document.getElementById('contacts-list');
  contactsList?.addEventListener('click', (e: Event) => {
    const target = e.target as HTMLElement;
    const editId = target.getAttribute('data-edit');
    const deleteId = target.getAttribute('data-delete');
    const saveEditId = target.getAttribute('data-save-edit');
    const cancelEdit = target.hasAttribute('data-cancel-edit');

    if (editId) {
      state.editing = Number(editId);
      state.adding = false;
      render();
      document.getElementById('edit-name')?.focus();
    }
    if (deleteId) void handleDelete(Number(deleteId));
    if (saveEditId) void handleSaveEdit(Number(saveEditId));
    if (cancelEdit) {
      state.editing = null;
      render();
    }
  });

  const editName = document.getElementById('edit-name');
  const editEmail = document.getElementById('edit-email');
  const editPhone = document.getElementById('edit-phone');
  const editKeyHandler = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && state.editing !== null) void handleSaveEdit(state.editing);
    if (e.key === 'Escape') { state.editing = null; render(); }
  };
  editName?.addEventListener('keydown', editKeyHandler);
  editEmail?.addEventListener('keydown', editKeyHandler);
  editPhone?.addEventListener('keydown', editKeyHandler);
}

// ── Init ───────────────────────────────────────────────

document.body.style.margin = '0';
document.body.style.padding = '0';
document.body.style.background = '#0a2e1a';

render();

fetchContacts().then((contacts) => {
  state.contacts = contacts;
  state.loading = false;
  render();
}).catch(() => {
  state.loading = false;
  render();
});

setInterval(() => {
  const clock = document.getElementById('clock');
  if (clock) clock.textContent = formatTime();
}, 1000);
