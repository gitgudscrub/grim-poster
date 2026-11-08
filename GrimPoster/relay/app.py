from __future__ import annotations

import hmac
import json
import logging
import os
import re
from datetime import datetime, timezone
import time
import asyncio
import contextlib
import io
import secrets
from dataclasses import dataclass
from collections import defaultdict, deque
from pathlib import Path
from urllib.parse import parse_qsl, urlencode, urlparse, urlunparse

import grim_relay_notifier as event_notifier
from game2_messages import unscheduled_next_game_payload

# These hooks are implemented in the private Sharktower Events integration.
# A missing hook fails closed; an unscheduled request must never be inferred
# from an integration outage.

from aiohttp import ClientSession, ClientTimeout, FormData, web
from PIL import Image, ImageOps, UnidentifiedImageError

MAX_IMAGE_BYTES = int(os.getenv("MAX_IMAGE_BYTES", "9961472"))
MAX_CONTENT_CHARS = 500
RATE_WINDOW_SECONDS = 3600
RATE_MAX_REQUESTS = 20
MIN_REQUEST_INTERVAL = 8
WEBHOOK_URL = os.getenv("GRIM_POSTER_WEBHOOK_URL", "").strip()
# Independent, optional webhook for opt-in diagnostic reports.
DIAGNOSTICS_WEBHOOK_URL = os.getenv("GRIM_DIAGNOSTICS_WEBHOOK_URL", "").strip()
DIAGNOSTIC_WINDOW = 3600
DIAGNOSTIC_LIMIT = 3
DIAGNOSTIC_MIN_INTERVAL = 600
DIAGNOSTIC_MAX_BYTES = 2048
_diagnostic_requests: dict[str, deque[float]] = defaultdict(deque)
_diagnostic_last: dict[str, float] = {}
_diagnostic_re = re.compile(r"^[a-z_]{2,60}$")
_version_re = re.compile(r"^\d+\.\d+\.\d+$")

# Separate upload authorisation from the server-to-server Events API secret.
# Ordinary public Grim Poster uploads continue working without this header,
# but they can NEVER trigger a Game 2 notification.
REVEAL_SUBMIT_KEY = os.getenv("GRIM_REVEAL_SUBMIT_KEY", "").strip()
REVEAL_SUBMIT_HEADER = "X-Grim-Reveal-Notify-Key"
DISCORD_WEBHOOK_RE = re.compile(r"^/api/webhooks/\d+/[A-Za-z0-9._-]+/?$")
requests_by_ip: dict[str, deque[float]] = defaultdict(deque)
last_request_by_ip: dict[str, float] = {}
ACTIVE_UPLOADS = asyncio.Semaphore(4)
# Staged Grims are private, held in RAM, and expire without being posted.
STAGE_TTL = int(os.getenv("GRIM_STAGE_TTL_SECONDS", "1200"))
STAGE_LIMIT = int(os.getenv("GRIM_STAGE_MAX_COUNT", "12"))
MAX_STAGE_RAM_BYTES = int(os.getenv("GRIM_STAGE_MAX_RAM_BYTES", str(64 * 1024 * 1024)))
Image.MAX_IMAGE_PIXELS = 24_000_000

@dataclass
class StagedGrim:
    token: str
    image: bytes
    filename: str
    created: float
    posting: bool = False
    posted: bool = False

staged: dict[str, StagedGrim] = {}
staged_lock = asyncio.Lock()

# One-shot Game 2 requests are issued ONLY after a successful Discord Grim
# post and bound to its returned Discord message ID. A request needs both an
# unguessable ID and a secret, and expires if the user closes their session.
NOTICE_TTL = 3600
@dataclass
class AnnouncementProof:
    token: str
    message_id: str
    created: float
    sending: bool = False
    notified: bool = False
    announcement_message_id: str | None = None

announcement_proofs: dict[str, AnnouncementProof] = {}
notice_lock = asyncio.Lock()


async def new_announcement_proof(message_id: str | None) -> dict[str, str]:
    if not message_id or not message_id.isdecimal():
        logging.warning("Grim posted, but Discord supplied no message ID for Game 2")
        return {}
    async with notice_lock:
        prune_notices(time.monotonic())
        if len(announcement_proofs) >= 120:
            logging.warning("Game 2 proof capacity reached")
            return {}
        request_id = secrets.token_urlsafe(24)
        token = secrets.token_urlsafe(32)
        announcement_proofs[request_id] = AnnouncementProof(token, message_id, time.monotonic())
    return {"game2_request_id": request_id, "game2_token": token}


def prune_notices(now: float):
    for key, item in list(announcement_proofs.items()):
        if not item.sending and now - item.created > NOTICE_TTL:
            del announcement_proofs[key]


def require_discord_message_id(result: object) -> str:
    """Require evidence that Events created an announcement, not just an API ACK."""
    if isinstance(result, dict):
        result = result.get("discord_message_id")
    if isinstance(result, str) and result.isascii() and result.isdecimal() and int(result) > 0:
        return result
    raise RuntimeError("Events integration did not confirm a Discord announcement message ID")


async def event_schedule_status() -> str:
    """Current authoritative Events-bot state; never guess from weekday/time."""
    checker = getattr(event_notifier, "game2_schedule_status", None)
    if not callable(checker):
        raise RuntimeError("Events schedule-status integration is not installed")
    status = await asyncio.to_thread(checker)
    if status not in ("scheduled", "no_event"):
        raise RuntimeError("Events returned an invalid or unknown Game 2 schedule status")
    return status


async def announce_game2(request: web.Request, body: dict) -> web.Response:
    request_id = body.get("request_id")
    token = body.get("token")
    confirm_unscheduled = body.get("confirm_unscheduled", False)
    if (not isinstance(request_id, str) or not isinstance(token, str)
            or len(request_id) > 64 or len(token) > 128
            or not isinstance(confirm_unscheduled, bool)):
        raise web.HTTPBadRequest(text="Invalid announcement request")
    async with notice_lock:
        prune_notices(time.monotonic())
        item = announcement_proofs.get(request_id)
        if item is None or not hmac.compare_digest(item.token, token):
            raise web.HTTPNotFound(text="Announcement request has expired")
        if item.notified:
            return web.json_response({"ok": True, "already_announced": True,
                                      "discord_message_id": item.announcement_message_id})
        if item.sending:
            raise web.HTTPConflict(text="Game 2 announcement is already in progress")
        item.sending = True
        message_id = item.message_id
    try:
        status = await event_schedule_status()
        if status == "no_event" and not confirm_unscheduled:
            return web.json_response({"ok": False, "confirmation_required": True,
                                      "event_status": "no_event"})
        if status == "scheduled":
            result = await asyncio.to_thread(event_notifier.notify_successful_reveal,
                                             message_id, event_id=None)
        else:
            manual_sender = getattr(event_notifier, "notify_unscheduled_game2", None)
            if not callable(manual_sender):
                raise RuntimeError("Manual Game 2 announcement integration is not installed")
            result = await asyncio.to_thread(
                manual_sender, message_id, payload=unscheduled_next_game_payload()
            )
        announcement_message_id = require_discord_message_id(result)
        logging.info("Game 2 posted: mode=%s Discord message_id=%s", status,
                     announcement_message_id)
    except Exception:
        logging.exception("Game 2 announcement failed or could not be verified")
        raise web.HTTPBadGateway(text="Could not confirm a Game 2 announcement in Discord")
    finally:
        async with notice_lock:
            item.sending = False
    async with notice_lock:
        item.notified = True
        item.announcement_message_id = announcement_message_id
    return web.json_response({"ok": True, "event_status": status,
                              "discord_message_id": announcement_message_id})


def process_staged_image(source: bytes) -> bytes:
    """Convert the raw screenshot off the aiohttp event loop."""
    try:
        with Image.open(io.BytesIO(source)) as opened:
            if (opened.format or "").lower() not in {"png", "jpeg", "webp", "bmp"}:
                raise ValueError("Unsupported image format")
            opened.load()
            oriented = ImageOps.exif_transpose(opened)
            if oriented.mode == "RGBA" or "transparency" in oriented.info:
                image = Image.new("RGB", oriented.size, (17, 17, 17))
                rgba = oriented.convert("RGBA")
                image.paste(rgba, mask=rgba.getchannel("A"))
            else:
                image = oriented.convert("RGB")
            for quality in (92, 76):
                result = io.BytesIO()
                image.save(result, format="JPEG", quality=quality, optimize=True)
                if result.tell() <= MAX_IMAGE_BYTES:
                    return result.getvalue()
            image.thumbnail((int(image.width * .75), int(image.height * .75)))
            result = io.BytesIO()
            image.save(result, format="JPEG", quality=76, optimize=True)
            if result.tell() <= MAX_IMAGE_BYTES:
                return result.getvalue()
            raise ValueError("The screenshot is too large for Discord")
    except (UnidentifiedImageError, OSError, Image.DecompressionBombError) as exc:
        raise ValueError("Invalid screenshot image") from exc


def prune_staged(now: float):
    for ident, item in list(staged.items()):
        # Keep tiny posted markers for a day to reject late duplicate commits.
        max_age = 86400 if item.posted else STAGE_TTL
        if not item.posting and now - item.created >= max_age:
            del staged[ident]


async def read_parts(request: web.Request, kinds: set[str]) -> tuple[dict[str, str], bytes | None, str]:
    reader = await request.multipart()
    fields: dict[str, str] = {}
    image = None
    filename = "botc-grimoire.png"
    async for part in reader:
        if part.name in {"kind", "content", "event_id"}:
            fields[part.name] = (await part.text())[:MAX_CONTENT_CHARS + 1]
        elif part.name == "file" and image is None:
            filename = Path(part.filename or filename).name[:120]
            chunks = bytearray()
            while chunk := await part.read_chunk(size=64 * 1024):
                if len(chunks) + len(chunk) > MAX_IMAGE_BYTES:
                    raise web.HTTPRequestEntityTooLarge(max_size=MAX_IMAGE_BYTES, actual_size=len(chunks) + len(chunk))
                chunks.extend(chunk)
            image = bytes(chunks)
    if fields.get("kind") not in kinds or image is None:
        raise web.HTTPBadRequest(text="A screenshot upload is required")
    if not image_media_type(image):
        raise web.HTTPBadRequest(text="Only PNG, JPEG, WebP, or BMP screenshots are accepted")
    return fields, image, filename


async def stage_grim(request: web.Request) -> web.Response:
    if not consume_rate_limit(request.headers.get("X-Real-IP", request.remote or "unknown")[:64]):
        raise web.HTTPTooManyRequests(text="Please wait before staging another screenshot")
    async with ACTIVE_UPLOADS:
        _, raw, _ = await read_parts(request, {"stage"})
        try:
            prepared = await asyncio.to_thread(process_staged_image, raw)
        except ValueError as exc:
            raise web.HTTPBadRequest(text=str(exc)) from exc
        now = time.monotonic()
        async with staged_lock:
            prune_staged(now)
            if len(staged) >= STAGE_LIMIT or sum(len(s.image) for s in staged.values()) + len(prepared) > MAX_STAGE_RAM_BYTES:
                raise web.HTTPServiceUnavailable(text="Screenshot staging capacity reached")
            stage_id = secrets.token_urlsafe(24)
            token = secrets.token_urlsafe(32)
            staged[stage_id] = StagedGrim(token, prepared, "botc-grimoire.jpg", now)
        return web.json_response({"ok": True, "stage_id": stage_id, "stage_token": token})


async def commit_grim(request: web.Request, body: dict) -> web.Response:
    stage_id = body.get("stage_id", "")
    token = body.get("stage_token", "")
    content = body.get("content", "")
    if not isinstance(stage_id, str) or not isinstance(token, str) or not isinstance(content, str) or len(content) > MAX_CONTENT_CHARS:
        raise web.HTTPBadRequest(text="Invalid staged screenshot request")
    if len(stage_id) > 64 or len(token) > 128 or not stage_id or not token:
        raise web.HTTPBadRequest(text="Invalid staged screenshot identifier")
    notify_game2 = authorised_reveal(request)
    async with staged_lock:
        prune_staged(time.monotonic())
        item = staged.get(stage_id)
        if item is None or not hmac.compare_digest(item.token, token):
            raise web.HTTPNotFound(text="Staged screenshot has expired or is unavailable")
        if item.posted:
            return web.json_response({"ok": True, "already_posted": True, **getattr(item, "notice_result", {})})
        if item.posting:
            raise web.HTTPConflict(text="Staged screenshot is already being posted")
        item.posting = True
        image = item.image
        filename = item.filename
    try:
        message_id = await send_to_discord(content, image, filename, "image/jpeg", return_message_id=True)
    except Exception:
        async with staged_lock:
            item.posting = False
        raise
    async with staged_lock:
        item.posting = False
        item.posted = True
        item.image = b""  # Retain a small idempotency record; discard private image.
    if notify_game2:
        if message_id and message_id.isdecimal():
            try:
                await asyncio.to_thread(event_notifier.notify_successful_reveal, message_id, event_id=None)
            except Exception:
                logging.exception("Game 2 notifier failed after staged Discord post")
        else:
            logging.warning("Staged Grim posted without a Discord message ID; Game 2 notice skipped")
        return web.json_response({"ok": True})
    proof = await new_announcement_proof(message_id)
    async with staged_lock:
        item.notice_result = proof
    return web.json_response({"ok": True, **proof})



def valid_webhook(value: str) -> bool:
    try:
        parsed = urlparse(value)
        return (
            parsed.scheme == "https"
            and parsed.hostname == "discord.com"
            and bool(DISCORD_WEBHOOK_RE.fullmatch(parsed.path))
            and not parsed.params
        )
    except ValueError:
        return False


def authorised_reveal(request: web.Request) -> bool:
    """Only deliberately authenticated uploads can send Game 2 alerts.

    The public relay is NOT a trustworthy Game 2 event signal on its own.
    Malformed explicit requests fail closed before posting to Discord.
    """
    submitted = request.headers.get(REVEAL_SUBMIT_HEADER, "")
    if not submitted:
        return False
    if len(REVEAL_SUBMIT_KEY) < 32 or not hmac.compare_digest(REVEAL_SUBMIT_KEY, submitted):
        raise web.HTTPForbidden(text="Unauthorised Game 2 reveal notification")
    return True


def discord_wait_url(value: str) -> str:
    """Ask Discord to return the posted message ID for a trusted reveal."""
    parsed = urlparse(value)
    query = [(k, v) for k, v in parse_qsl(parsed.query, keep_blank_values=True)
             if k != "wait"]
    query.append(("wait", "true"))
    return urlunparse(parsed._replace(query=urlencode(query)))


def consume_rate_limit(ip: str, now: float | None = None) -> bool:
    now = time.monotonic() if now is None else now
    recent = requests_by_ip[ip]
    while recent and recent[0] <= now - RATE_WINDOW_SECONDS:
        recent.popleft()
    previous = last_request_by_ip.get(ip)
    if len(recent) >= RATE_MAX_REQUESTS or (
        previous is not None and now - previous < MIN_REQUEST_INTERVAL
    ):
        return False
    recent.append(now)
    last_request_by_ip[ip] = now
    return True


def image_media_type(data: bytes) -> str | None:
    if data.startswith(b"\x89PNG\r\n\x1a\n"):
        return "image/png"
    if data.startswith(b"\xff\xd8\xff"):
        return "image/jpeg"
    if data.startswith(b"RIFF") and data[8:12] == b"WEBP":
        return "image/webp"
    if data.startswith(b"BM"):
        return "image/bmp"
    return None


@web.middleware
async def cors_and_errors(request: web.Request, handler):
    if request.method == "OPTIONS":
        response = web.Response(status=204)
    else:
        try:
            response = await handler(request)
        except web.HTTPException as exc:
            response = web.json_response({"error": exc.reason}, status=exc.status)
        except Exception:
            logging.exception("relay request failed")
            response = web.json_response({"error": "Upload relay failed"}, status=500)
    response.headers["Access-Control-Allow-Origin"] = "*"
    response.headers["Access-Control-Allow-Methods"] = "POST, OPTIONS"
    response.headers["Access-Control-Allow-Headers"] = "Content-Type, X-Grim-Reveal-Notify-Key, X-Grim-Stage"
    response.headers["Cache-Control"] = "no-store"
    return response


async def send_to_discord(content: str, image: bytes | None = None, filename: str = "botc-grimoire.png", media_type: str = "image/png", *, return_message_id: bool = False) -> str | None:
    if not valid_webhook(WEBHOOK_URL):
        raise web.HTTPServiceUnavailable(text="The Discord relay is not configured")
    payload = {
        "username": "Grim Poster",
        "allowed_mentions": {"parse": []},
        "content": content,
    }
    timeout = ClientTimeout(total=25)
    target_url = discord_wait_url(WEBHOOK_URL) if return_message_id else WEBHOOK_URL
    async with ClientSession(timeout=timeout) as session:
        if image is None:
            async with session.post(target_url, json=payload) as response:
                if response.status >= 300:
                    raise web.HTTPBadGateway(text=f"Discord returned HTTP {response.status}")
                if return_message_id:
                    try:
                        message = await response.json(content_type=None)
                        return str(message.get("id", "")) or None if isinstance(message, dict) else None
                    except (ValueError, TypeError):
                        logging.warning("Discord upload succeeded but returned no parseable message ID")
                        return None
        else:
            form = FormData()
            form.add_field("payload_json", json.dumps(payload), content_type="application/json")
            form.add_field("files[0]", image, filename=filename, content_type=media_type)
            async with session.post(target_url, data=form) as response:
                if response.status >= 300:
                    raise web.HTTPBadGateway(text=f"Discord returned HTTP {response.status}")
                if return_message_id:
                    try:
                        message = await response.json(content_type=None)
                        return str(message.get("id", "")) or None if isinstance(message, dict) else None
                    except (ValueError, TypeError):
                        logging.warning("Discord upload succeeded but returned no parseable message ID")
                        return None
    return None


async def upload(request: web.Request) -> web.Response:
    # Same public URL as older clients so no reverse-proxy change is necessary.
    kind = request.headers.get("Content-Type", "")
    if kind.startswith("application/json"):
        try:
            body = await request.json()
        except Exception as exc:
            raise web.HTTPBadRequest(text="Invalid JSON body") from exc
        if isinstance(body, dict) and body.get("kind") == "commit":
            return await commit_grim(request, body)
        if isinstance(body, dict) and body.get("kind") == "announce_game2":
            return await announce_game2(request, body)
    if request.content_type == "multipart/form-data":
        # The field parser is consumed once only. The desktop sends stage via
        # this explicit header; legacy clients need no modifications.
        if request.headers.get("X-Grim-Stage") == "1":
            return await stage_grim(request)
    if not consume_rate_limit(request.headers.get("X-Real-IP", request.remote or "unknown")[:64]):
        raise web.HTTPTooManyRequests(text="Please wait before sending another Grim Poster request")
    async with ACTIVE_UPLOADS:
        return await process_upload(request)


async def process_upload(request: web.Request) -> web.Response:
    # Check authentication before any webhook mutation. Public uploads work
    # exactly as before; only authorised uploads can signal a completed reveal.
    notify_game2 = authorised_reveal(request)
    content_type = request.headers.get("Content-Type", "")
    if content_type.startswith("application/json"):
        try:
            body = await request.json()
        except Exception as exc:
            raise web.HTTPBadRequest(text="Invalid JSON body") from exc
        if not isinstance(body, dict) or body.get("kind") != "test":
            raise web.HTTPBadRequest(text="Unsupported request")
        await send_to_discord("Grim Poster is connected and ready.")
        return web.json_response({"ok": True})

    fields, image, filename = await read_parts(request, {"upload"})
    media_type = image_media_type(image)
    if not media_type:
        raise web.HTTPBadRequest(text="Only PNG, JPEG, WebP, or BMP screenshots are accepted")
    if len(fields.get("content", "")) > MAX_CONTENT_CHARS:
        raise web.HTTPBadRequest(text="Message text is too long")
    event_id = None
    if notify_game2 and fields.get("event_id"):
        raw_event_id = fields["event_id"]
        if not raw_event_id.isascii() or not raw_event_id.isdecimal() or int(raw_event_id) < 1:
            raise web.HTTPBadRequest(text="Invalid target event_id")
        event_id = int(raw_event_id)

    message_id = await send_to_discord(fields.get("content", ""), image, filename,
                                      media_type, return_message_id=True)
    # The Discord upload has succeeded. Events may be unavailable, but that
    # never turns a successful Grim reveal into an upload failure.
    if notify_game2:
        if message_id and message_id.isdecimal():
            try:
                await asyncio.to_thread(event_notifier.notify_successful_reveal, message_id, event_id=event_id)
            except Exception:
                # Don't ever undo a successful Discord upload because Events
                # is offline or the optional notifier encounters a code error.
                logging.exception("Game 2 notifier crashed after successful Discord Grim post")
        else:
            logging.warning("Grim reveal reached Discord, but no stable message ID was returned; Game 2 notice skipped")
        return web.json_response({"ok": True})
    proof = await new_announcement_proof(message_id)
    return web.json_response({"ok": True, **proof})


async def expiry_sweeper(app: web.Application):
    while True:
        await asyncio.sleep(60)
        async with staged_lock:
            prune_staged(time.monotonic())
        async with notice_lock:
            prune_notices(time.monotonic())


async def begin_sweeper(app: web.Application):
    app["stage_sweeper"] = asyncio.create_task(expiry_sweeper(app))


async def stop_sweeper(app: web.Application):
    task = app["stage_sweeper"]
    task.cancel()
    with contextlib.suppress(asyncio.CancelledError):
        await task
    async with staged_lock:
        staged.clear()


async def diagnostic_report(request: web.Request) -> web.Response:
    """Explicitly consented structured error reports; never accept raw logs.

    This is an intentionally separate route and Discord destination from Grim
    screenshot uploads. Limit accepted fields to a short allow-list.
    """
    if request.method != "POST":
        raise web.HTTPMethodNotAllowed(request.method, ["POST"])
    if not valid_webhook(DIAGNOSTICS_WEBHOOK_URL):
        raise web.HTTPServiceUnavailable(text="Diagnostic collection is not configured")
    if request.headers.get("X-Grim-Diagnostics-Consent") != "yes":
        raise web.HTTPForbidden(text="Explicit report consent is required")
    if request.headers.get("Content-Type", "").split(";", 1)[0].lower() != "application/json":
        raise web.HTTPUnsupportedMediaType(text="Expected JSON")
    if request.content_length is not None and request.content_length > DIAGNOSTIC_MAX_BYTES:
        raise web.HTTPRequestEntityTooLarge(max_size=DIAGNOSTIC_MAX_BYTES, actual_size=request.content_length)
    body = await request.content.read(DIAGNOSTIC_MAX_BYTES + 1)
    if len(body) > DIAGNOSTIC_MAX_BYTES:
        raise web.HTTPRequestEntityTooLarge(max_size=DIAGNOSTIC_MAX_BYTES, actual_size=len(body))
    try:
        data = json.loads(body)
    except (ValueError, TypeError):
        raise web.HTTPBadRequest(text="Invalid JSON")
    required = {"time", "version", "event", "category", "result"}
    if not isinstance(data, dict) or set(data) != required:
        raise web.HTTPBadRequest(text="Unexpected diagnostic fields")
    if not _version_re.fullmatch(str(data["version"])):
        raise web.HTTPBadRequest(text="Invalid version")
    safe_events = {"launcher_start", "launcher_start_failed", "operation_failed", "patch_install_failed", "companion_launch_failed", "companion_spawn_failed", "companion_exited", "uncaught_panic", "other_event"}
    safe_categories = {"none", "permission_denied", "access_denied", "file_in_use", "path_missing", "network_timeout", "integrity_failure", "missing_file", "other_error"}
    if not all(isinstance(data[k], str) for k in required) or data["event"] not in safe_events or data["category"] not in safe_categories or data["result"] not in ("ok", "error"):
        raise web.HTTPBadRequest(text="Invalid diagnostic category")
    try:
        parsed_time = datetime.fromisoformat(data["time"].replace("Z", "+00:00"))
        if parsed_time.tzinfo is None or abs((datetime.now(timezone.utc)-parsed_time).total_seconds()) > 7*86400:
            raise ValueError("timestamp outside accepted range")
    except (ValueError, TypeError, AttributeError):
        raise web.HTTPBadRequest(text="Invalid timestamp")

    ip = request.headers.get("X-Real-IP", request.remote or "unknown")[:64]
    now = time.monotonic()
    recent = _diagnostic_requests[ip]
    while recent and recent[0] < now-DIAGNOSTIC_WINDOW:
        recent.popleft()
    if len(recent)>=DIAGNOSTIC_LIMIT or now-_diagnostic_last.get(ip, -1e9) < DIAGNOSTIC_MIN_INTERVAL:
        raise web.HTTPTooManyRequests(text="Diagnostic rate limit reached")
    recent.append(now)
    _diagnostic_last[ip]=now
    # Never forward IP, full machine identity, usernames or raw stderr to Discord.
    message = "Grim Poster opt-in diagnostic\n" + "\n".join(
        f"{k}: {data[k]}" for k in ("time", "version", "event", "category", "result")
    )
    timeout = ClientTimeout(total=10)
    async with ClientSession(timeout=timeout) as session:
        async with session.post(DIAGNOSTICS_WEBHOOK_URL, json={
            "username": "Grim Poster Diagnostics", "content": message,
            "allowed_mentions": {"parse": []},
        }) as response:
            if response.status >= 300:
                raise web.HTTPBadGateway(text="Diagnostics delivery failed")
    return web.json_response({"ok": True}, status=202)


def create_app() -> web.Application:
    app = web.Application(client_max_size=MAX_IMAGE_BYTES + 64_000, middlewares=[cors_and_errors])
    app.router.add_route("*", "/upload", upload)
    app.router.add_post("/diagnostics", diagnostic_report)
    app.on_startup.append(begin_sweeper)
    app.on_cleanup.append(stop_sweeper)
    return app


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    web.run_app(create_app(), host=os.getenv("HOST", "127.0.0.1"), port=int(os.getenv("PORT", "8766")), access_log=None)
