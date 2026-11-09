import asyncio
import sys

from . import __version__
from .main import main

if __name__ == "__main__":
    if "--version" in sys.argv:
        print(__version__)
        raise SystemExit(0)
    asyncio.run(main())
