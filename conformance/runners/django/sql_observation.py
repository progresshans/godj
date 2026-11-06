"""Django SQL capture mechanics with caller-owned statement classification."""

from collections.abc import Callable
from typing import Any


def capture_statements(
    database_connection: Any,
    operation: Callable[[], Any],
    *,
    classify: Callable[[str], str],
) -> tuple[Any, dict[str, Any]]:
    statements: list[str] = []

    def wrapper(execute, sql, params, many, context):
        statements.append(classify(sql))
        return execute(sql, params, many, context)

    with database_connection.execute_wrapper(wrapper):
        result = operation()
    return result, {
        "query_count": len(statements),
        "statement_kinds": statements,
    }
