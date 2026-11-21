import { memo, useEffect, useRef, useState } from "react";

export type PanelKind = "discover" | "listen" | "satellites" | "decoder" | "demod";

export interface ExploreMenuProps {
  onPick: (k: "discover" | "listen" | "satellites") => void;
  /** a panel is open / something is running (highlights the button) */
  active: boolean;
}

const ITEMS: { k: "discover" | "listen" | "satellites"; icon: string; name: string; desc: string; tip: string }[] = [
  {
    k: "discover",
    icon: "🔍",
    name: "Discover",
    desc: "2–3 min report: what can my antenna receive?",
    tip: "Discover: a one-off survey that checks each band and decoder that fits your antenna, then shows a report. Your tuning is restored afterwards.",
  },
  {
    k: "listen",
    icon: "🎧",
    name: "Listen live",
    desc: "plays transmissions as they happen (scanner)",
    tip: "Listen live: the scanner watches a band and plays each transmission as it happens. Runs until you stop it.",
  },
  {
    k: "satellites",
    icon: "🛰",
    name: "Satellites",
    desc: "passes, autopilot, SSTV",
    tip: "Satellites: upcoming passes, the pass autopilot and image decoders.",
  },
];

/** "Explore" dropdown: Enter/Space/ArrowDown opens, arrows move, Esc closes, outside click closes. */
export const ExploreMenu = memo(function ExploreMenu({ onPick, active }: ExploreMenuProps) {
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLSpanElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  const items = useRef<(HTMLButtonElement | null)[]>([]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const openMenu = () => {
    setOpen(true);
    window.setTimeout(() => items.current[0]?.focus(), 0);
  };
  const close = (refocus: boolean) => {
    setOpen(false);
    if (refocus) btn.current?.focus();
  };
  const move = (from: number, d: number) => {
    const n = ITEMS.length;
    items.current[(from + d + n) % n]?.focus();
  };

  return (
    <span className="explore" ref={wrap}>
      <button
        ref={btn}
        className={active || open ? "primary on" : "primary"}
        aria-haspopup="menu"
        aria-expanded={open}
        title="Explore: Discover (one-off report), Listen live (scanner) or Satellites"
        onClick={() => (open ? close(false) : openMenu())}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown" && !open) {
            e.preventDefault();
            openMenu();
          } else if (e.key === "Escape" && open) close(false);
        }}
      >
        ◎ Explore ▾
      </button>
      {open && (
        <div
          className="explore-menu"
          role="menu"
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              close(true);
            }
          }}
        >
          {ITEMS.map((it, i) => (
            <button
              key={it.k}
              role="menuitem"
              ref={(el) => {
                items.current[i] = el;
              }}
              title={it.tip}
              onClick={() => {
                close(false);
                onPick(it.k);
              }}
              onKeyDown={(e) => {
                if (e.key === "ArrowDown") {
                  e.preventDefault();
                  move(i, 1);
                } else if (e.key === "ArrowUp") {
                  e.preventDefault();
                  move(i, -1);
                } else if (e.key === "Home") {
                  e.preventDefault();
                  items.current[0]?.focus();
                } else if (e.key === "End") {
                  e.preventDefault();
                  items.current[ITEMS.length - 1]?.focus();
                } else if (e.key === "Tab") setOpen(false);
              }}
            >
              <span className="ico" aria-hidden>{it.icon}</span>
              <span>
                <b>{it.name}</b>
                <span className="desc">{it.desc}</span>
              </span>
            </button>
          ))}
        </div>
      )}
    </span>
  );
});

export interface RadioOwner {
  kind: PanelKind | "idle";
  /** text after "radio:" */
  text: string;
}

export interface RadioPillProps {
  kind: RadioOwner["kind"];
  text: string;
  onOpen: (k: PanelKind) => void;
  onStop: (k: PanelKind) => void;
}

const PILL_TIP =
  "Who controls the radio right now. Discover = one-off report of what your antenna can receive; Listen live = the scanner plays transmissions as they happen. Click to open its panel.";

/** Memoized: re-renders only when kind/text change (callbacks must be stable). */
export const RadioPill = memo(function RadioPill({ kind, text, onOpen, onStop }: RadioPillProps) {
  const busy = kind !== "idle";
  return (
    <span className={`radio-pill ${busy ? "busy" : ""}`} title={PILL_TIP}>
      <button
        className="pill-main"
        disabled={!busy}
        onClick={() => kind !== "idle" && onOpen(kind)}
        title={busy ? `${text} (click to open)` : PILL_TIP}
      >
        <span className="pill-label">radio:</span> {busy && kind !== "demod" && <span className="pill-dot">●</span>} {text}
      </button>
      {busy && (
        <button className="linkbtn pill-stop" onClick={() => onStop(kind)} title="Stop this activity and free the radio">
          stop
        </button>
      )}
    </span>
  );
});
