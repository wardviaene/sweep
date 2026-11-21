import { useEffect, useRef, type ReactNode } from "react";

/** A button that toggles a dropdown panel; closes on outside click / Escape. */
export function Popover(props: {
  label: ReactNode;
  title?: string;
  open: boolean;
  setOpen: (o: boolean) => void;
  children: ReactNode;
  width?: number;
}) {
  const { open, setOpen } = props;
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, setOpen]);
  return (
    <span className="pop" ref={ref}>
      <button className={open ? "on" : ""} title={props.title} onClick={() => setOpen(!open)}>
        {props.label}
      </button>
      {/* kept mounted so a running scan/record keeps its state while the panel is closed */}
      <div className="pop-panel" style={{ width: props.width ?? 340, display: open ? "block" : "none" }}>
        {props.children}
      </div>
    </span>
  );
}
