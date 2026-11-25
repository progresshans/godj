"""Private PostgreSQL databases for pinned native Django observations."""
from contextlib import contextmanager
import secrets


@contextmanager
def private_database(control, variable):
    from psycopg import sql

    prefix = variable.removesuffix("_DATABASE").lower()
    name = f"{prefix}_{secrets.randbits(64)}"
    identifier = sql.Identifier(name)
    control.execute(sql.SQL("CREATE DATABASE {} TEMPLATE template0").format(identifier))
    try:
        yield name
    finally:
        control.execute(sql.SQL("DROP DATABASE {}").format(identifier))
