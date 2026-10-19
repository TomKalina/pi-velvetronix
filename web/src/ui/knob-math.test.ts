import { describe, expect, it } from "vitest";
import { arcPath, dragValue, DRAG_FULL_PX, keyValue, knobStep, snap, valueToAngle, wheelValue } from "./knob-math";

describe("knob math", () => {
  it("maps the range onto a 270 degree sweep", () => {
    expect(valueToAngle(0, 0, 100)).toBe(-135);
    expect(valueToAngle(50, 0, 100)).toBe(0);
    expect(valueToAngle(100, 0, 100)).toBe(135);
    expect(valueToAngle(30, 30, 1200)).toBe(-135);
    expect(valueToAngle(-5, 0, 100)).toBe(-135);
    expect(valueToAngle(500, 0, 100)).toBe(135);
    expect(valueToAngle(3, 3, 3)).toBe(-135);
  });

  it("picks coarser steps for wide ranges", () => {
    expect(knobStep(0, 100)).toBe(1);
    expect(knobStep(0, 70)).toBe(1);
    expect(knobStep(30, 1200)).toBe(5);
    expect(knobStep(100, 10000)).toBe(50);
  });

  it("snaps to the step grid anchored at min and clamps", () => {
    expect(snap(52.4, 0, 100, 1)).toBe(52);
    expect(snap(33, 30, 1200, 5)).toBe(35);
    expect(snap(1199, 30, 1200, 5)).toBe(1200);
    expect(snap(5000, 1650, 10000, 50)).toBe(5000);
    expect(snap(-3, 0, 100, 1)).toBe(0);
  });

  it("drags up to increase, full range over DRAG_FULL_PX, 4x finer with shift", () => {
    expect(dragValue(50, -DRAG_FULL_PX / 2, 0, 100, 1, false)).toBe(100);
    expect(dragValue(50, DRAG_FULL_PX, 0, 100, 1, false)).toBe(0);
    expect(dragValue(50, -22, 0, 100, 1, false)).toBe(60);
    expect(dragValue(50, -22, 0, 100, 1, true)).toBe(53);
    expect(dragValue(50, 0, 0, 100, 1, false)).toBe(50);
    expect(dragValue(100, -DRAG_FULL_PX, 100, 10000, 50, false)).toBe(10000);
  });

  it("wheel: one notch is one step, up increases", () => {
    expect(wheelValue(50, -100, 0, 100, 1)).toBe(51);
    expect(wheelValue(50, 3, 0, 100, 1)).toBe(49);
    expect(wheelValue(50, 0, 0, 100, 1)).toBe(50);
    expect(wheelValue(100, -1, 0, 100, 1)).toBe(100);
  });

  it("keyboard follows the WAI-ARIA slider pattern", () => {
    expect(keyValue("ArrowUp", 50, 0, 100, 1)).toBe(51);
    expect(keyValue("ArrowRight", 50, 0, 100, 1)).toBe(51);
    expect(keyValue("ArrowDown", 50, 0, 100, 1)).toBe(49);
    expect(keyValue("ArrowLeft", 0, 0, 100, 1)).toBe(0);
    expect(keyValue("PageUp", 95, 0, 100, 1)).toBe(100);
    expect(keyValue("PageDown", 50, 0, 100, 1)).toBe(40);
    expect(keyValue("Home", 50, 30, 1200, 5)).toBe(30);
    expect(keyValue("End", 50, 30, 1200, 5)).toBe(1200);
    expect(keyValue("PageUp", 400, 100, 10000, 50)).toBe(900);
    expect(keyValue("Enter", 50, 0, 100, 1)).toBeNull();
    expect(keyValue("a", 50, 0, 100, 1)).toBeNull();
  });

  it("draws arcs clockwise from 12 o'clock", () => {
    expect(arcPath(50, 50, 40, 0, 90)).toBe("M 50.00 10.00 A 40 40 0 0 1 90.00 50.00");
    expect(arcPath(50, 50, 40, -135, 135)).toContain(" 1 1 ");
    expect(arcPath(50, 50, 40, -135, -135)).toBe("M 21.72 78.28");
  });
});
