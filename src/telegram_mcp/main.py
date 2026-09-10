"""stdio entry point.

stdout belongs to the MCP protocol, so every log line is pushed to stderr.
"""

import logging
import sys

from telegram_mcp.tools import mcp


def main() -> None:
    logging.basicConfig(
        stream=sys.stderr,
        level=logging.WARNING,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
