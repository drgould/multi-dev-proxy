import os
from typing import Optional

import psycopg2
import psycopg2.extras
import strawberry
from fastapi import FastAPI
from strawberry.fastapi import GraphQLRouter


def get_conn():
    return psycopg2.connect(os.environ["DATABASE_URL"])


@strawberry.type
class Book:
    id: int
    title: str
    author: str
    year: int


@strawberry.input
class BookInput:
    title: str
    author: str
    year: int


@strawberry.type
class Query:
    @strawberry.field
    def books(self, search: Optional[str] = None) -> list[Book]:
        conn = get_conn()
        cur = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
        if search:
            cur.execute(
                "SELECT * FROM books WHERE title ILIKE %s OR author ILIKE %s ORDER BY title",
                (f"%{search}%", f"%{search}%"),
            )
        else:
            cur.execute("SELECT * FROM books ORDER BY title")
        rows = cur.fetchall()
        cur.close()
        conn.close()
        return [Book(**r) for r in rows]

    @strawberry.field
    def book(self, id: int) -> Optional[Book]:
        conn = get_conn()
        cur = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
        cur.execute("SELECT * FROM books WHERE id = %s", (id,))
        row = cur.fetchone()
        cur.close()
        conn.close()
        return Book(**row) if row else None


@strawberry.type
class Mutation:
    @strawberry.mutation
    def add_book(self, input: BookInput) -> Book:
        conn = get_conn()
        cur = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
        cur.execute(
            "INSERT INTO books (title, author, year) VALUES (%s, %s, %s) RETURNING *",
            (input.title, input.author, input.year),
        )
        row = cur.fetchone()
        conn.commit()
        cur.close()
        conn.close()
        return Book(**row)

    @strawberry.mutation
    def delete_book(self, id: int) -> bool:
        conn = get_conn()
        cur = conn.cursor()
        cur.execute("DELETE FROM books WHERE id = %s", (id,))
        deleted = cur.rowcount > 0
        conn.commit()
        cur.close()
        conn.close()
        return deleted


schema = strawberry.Schema(query=Query, mutation=Mutation)
graphql_app = GraphQLRouter(schema)

app = FastAPI()
app.include_router(graphql_app, prefix="/graphql")


@app.get("/health")
def health():
    return {"ok": True}
