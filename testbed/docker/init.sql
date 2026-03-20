CREATE TABLE IF NOT EXISTS books (
    id SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    author TEXT NOT NULL,
    year INTEGER NOT NULL
);

INSERT INTO books (title, author, year) VALUES
    ('The Pragmatic Programmer', 'David Thomas & Andrew Hunt', 1999),
    ('Designing Data-Intensive Applications', 'Martin Kleppmann', 2017),
    ('Clean Code', 'Robert C. Martin', 2008),
    ('Refactoring', 'Martin Fowler', 2018),
    ('Structure and Interpretation of Computer Programs', 'Harold Abelson & Gerald Sussman', 1984);
