import express from 'express';
import cors from 'cors';
import Database from 'better-sqlite3';
import path from 'path';

// ── Types ──────────────────────────────────────────────

interface Contact {
  id: number;
  name: string;
  email: string;
  phone: string;
  created_at: string;
}

// ── Database ───────────────────────────────────────────

const port = parseInt(process.env.API_PORT || '0') || (parseInt(process.env.PORT || '5173') + 1);
const dbPath = path.join(import.meta.dirname, 'contacts.db');
const db = new Database(dbPath);

db.pragma('journal_mode = WAL');
db.exec(`
  CREATE TABLE IF NOT EXISTS contacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    phone TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
  )
`);

// Seed some data if empty
const count = db.prepare('SELECT COUNT(*) as n FROM contacts').get() as { n: number };
if (count.n === 0) {
  const insert = db.prepare('INSERT INTO contacts (name, email, phone) VALUES (?, ?, ?)');
  insert.run('Alice Johnson', 'alice@example.com', '555-0101');
  insert.run('Bob Smith', 'bob@example.com', '555-0102');
  insert.run('Carol Williams', 'carol@example.com', '555-0103');
}

// ── Express ────────────────────────────────────────────

const app = express();
app.use(cors());
app.use(express.json());

// List all
app.get('/api/contacts', (_req, res) => {
  const rows = db.prepare('SELECT * FROM contacts ORDER BY name').all();
  res.json(rows);
});

// Get one
app.get('/api/contacts/:id', (req, res) => {
  const row = db.prepare('SELECT * FROM contacts WHERE id = ?').get(req.params.id);
  if (!row) return res.status(404).json({ error: 'not found' });
  res.json(row);
});

// Create
app.post('/api/contacts', (req, res) => {
  const { name, email, phone } = req.body;
  if (!name) return res.status(400).json({ error: 'name is required' });
  const result = db.prepare('INSERT INTO contacts (name, email, phone) VALUES (?, ?, ?)').run(name, email || '', phone || '');
  const created = db.prepare('SELECT * FROM contacts WHERE id = ?').get(result.lastInsertRowid);
  res.status(201).json(created);
});

// Update
app.put('/api/contacts/:id', (req, res) => {
  const { name, email, phone } = req.body;
  const existing = db.prepare('SELECT * FROM contacts WHERE id = ?').get(req.params.id) as Contact | undefined;
  if (!existing) return res.status(404).json({ error: 'not found' });
  db.prepare('UPDATE contacts SET name = ?, email = ?, phone = ? WHERE id = ?').run(
    name ?? existing.name,
    email ?? existing.email,
    phone ?? existing.phone,
    req.params.id
  );
  const updated = db.prepare('SELECT * FROM contacts WHERE id = ?').get(req.params.id);
  res.json(updated);
});

// Delete
app.delete('/api/contacts/:id', (req, res) => {
  const result = db.prepare('DELETE FROM contacts WHERE id = ?').run(req.params.id);
  if (result.changes === 0) return res.status(404).json({ error: 'not found' });
  res.json({ ok: true });
});

app.listen(port, () => {
  console.log(`API server listening on http://localhost:${port}`);
});
