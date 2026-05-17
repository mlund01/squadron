from squadron_sdk import Squadron

app = Squadron()


@app.tool
def ping() -> str:
    """Returns 'pong'."""
    return "pong"


def main() -> None:
    app.serve()
