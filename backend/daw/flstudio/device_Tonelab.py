# name=Tonelab

# Tonelab's half inside FL Studio. FL's Python cannot open a socket or a
# file, so requests arrive as MIDI system exclusive on the "Tonelab" port
# and answers go back the same way. One JSON object per message, ASCII
# only, since sysex carries seven-bit bytes. Manufacturer byte 0x7D is the
# non-commercial id, so no real device claims it.

import json

import device
import general
import mixer
import plugins
import transport

TAG = bytes([0xF0, 0x7D])
END = bytes([0xF7])

# Mix properties by the names Tonelab uses. Panning is FL's -1..1 here; the
# other side folds it to 0..1.
GET = {
    "volume": mixer.getTrackVolume,
    "pan": mixer.getTrackPan,
    "mute": mixer.isTrackMuted,
    "solo": mixer.isTrackSolo,
}
SET = {
    "volume": mixer.setTrackVolume,
    "pan": mixer.setTrackPan,
    "mute": lambda t, v: mixer.muteTrack(t, int(bool(v))),
    "solo": lambda t, v: mixer.soloTrack(t, int(bool(v))),
}

MAX_SLOTS = 10


def reply(request, body):
    body["id"] = request.get("id")
    device.midiOutSysex(TAG + json.dumps(body, ensure_ascii=True).encode("ascii") + END)


def handle(request):
    op = request.get("op")
    if op == "ping":
        return {"ok": True, "version": general.getVersion()}
    if op == "tracks":
        return {"tracks": [{"n": t, "name": mixer.getTrackName(t)} for t in range(1, mixer.trackCount())]}
    if op == "undo":
        general.undo()
        return {"ok": True}
    if op == "play":
        transport.start()
        return {"ok": True}
    if op == "stop":
        transport.stop()
        return {"ok": True}
    track = int(request.get("track", -1))
    if track < 0 or track >= mixer.trackCount():
        return {"error": "no such track"}
    if op == "get":
        getter = GET.get(request.get("name"))
        if getter is None:
            return {"error": "no such parameter"}
        return {"value": getter(track)}
    if op == "set":
        setter = SET.get(request.get("name"))
        if setter is None:
            return {"error": "no such parameter"}
        setter(track, request.get("value"))
        return {"ok": True}
    if op == "route":
        mixer.setRouteToLevel(track, int(request.get("to")), float(request.get("value")))
        return {"ok": True}
    if op == "fx":
        # Names and counts only. A wrapped third-party plugin reports
        # thousands of parameters (measured: 4240 for an Audio Unit
        # reverb), and one sysex tops out between 64 and 128 KB, so the
        # names come by page.
        return {"fx": [{"slot": slot, "name": plugins.getPluginName(track, slot), "count": plugins.getParamCount(track, slot)}
                       for slot in range(MAX_SLOTS) if plugins.isValid(track, slot)]}
    slot = int(request.get("slot", -1))
    if op == "params":
        if not plugins.isValid(track, slot):
            return {"error": "no such effect"}
        start = int(request.get("from", 0))
        stop = min(start + int(request.get("n", 256)), plugins.getParamCount(track, slot))
        return {"params": [[p, plugins.getParamName(p, track, slot), plugins.getParamValueString(p, track, slot)] for p in range(start, stop)]}
    param = int(request.get("param", -1))
    if not plugins.isValid(track, slot) or param < 0 or param >= plugins.getParamCount(track, slot):
        return {"error": "no such effect parameter"}
    if op == "fxget":
        return {"value": plugins.getParamValue(param, track, slot), "str": plugins.getParamValueString(param, track, slot)}
    if op == "fxset":
        plugins.setParamValue(float(request.get("value")), param, track, slot)
        return {"ok": True}
    return {"error": "unknown op %r" % op}


def OnInit():
    print("Tonelab bridge ready")


def OnSysEx(event):
    raw = bytes(event.sysex)
    if not raw.startswith(TAG):
        return
    try:
        request = json.loads(raw[len(TAG):-1].decode("ascii"))
    except Exception as e:
        print("Tonelab: unreadable request: %s" % e)
        return
    event.handled = True
    try:
        reply(request, handle(request))
    except Exception as e:
        reply(request, {"error": str(e)})
