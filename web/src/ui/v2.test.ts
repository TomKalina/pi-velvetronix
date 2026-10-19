import { describe, expect, it } from "vitest";
import { filterSongs, presetName, searchKey, validateProgramName } from "./logic";
import { DEFAULT_PREFS, parsePrefs } from "./prefs";

describe("presetName", () => {
  it("strips diacritics, keeps printable ASCII and cuts at 16 chars", () => {
    expect(presetName("Svět jsou bary")).toBe("Svet jsou bary");
    expect(presetName("Všechno je naopak")).toBe("Vsechno je naopa");
    expect(presetName("Ever Fallen in Love (With Someone You Shouldn't've)")).toBe("Ever Fallen in L");
    expect(presetName("What’s My Age Again?")).toBe("What's My Age Ag");
    expect(presetName("Tři sestry – Extraliga")).toBe("Tri sestry Extra");
    expect(presetName("Ab ".padEnd(15, " ") + "x")).toBe("Ab x");
    for (const t of ["Žluťoučký kůň", "Ça va", "日本"]) expect(validateProgramName(presetName(t))).toBeNull();
  });
});

describe("filterSongs", () => {
  const songs = [
    { title: "Svět jsou bary", artist: "Plexis", genre: "czech-punk", year: 1990 },
    { title: "Basket Case", artist: "Green Day", genre: "pop-punk", year: 1994 },
    { title: "Traktor", artist: "Visací zámek", genre: "czech-punk", year: null },
  ];
  it("matches every word without diacritics, in title, artist or year", () => {
    expect(filterSongs(songs, "", null)).toHaveLength(3);
    expect(filterSongs(songs, "svet", null).map((s) => s.title)).toEqual(["Svět jsou bary"]);
    expect(filterSongs(songs, "ZAMEK", null).map((s) => s.title)).toEqual(["Traktor"]);
    expect(filterSongs(songs, "green 1994", null)).toHaveLength(1);
    expect(filterSongs(songs, "green 1990", null)).toHaveLength(0);
    expect(filterSongs(songs, "", "czech-punk")).toHaveLength(2);
    expect(filterSongs(songs, "basket", "czech-punk")).toHaveLength(0);
    expect(searchKey("Říp")).toBe("rip");
  });
});

describe("prefs", () => {
  it("defaults to graphic view with original names", () => {
    expect(DEFAULT_PREFS).toEqual({ display: "graphic", names: "original" });
    expect(parsePrefs(null)).toEqual(DEFAULT_PREFS);
    expect(parsePrefs("not json")).toEqual(DEFAULT_PREFS);
    expect(parsePrefs('{"display":"weird","names":5}')).toEqual(DEFAULT_PREFS);
    expect(parsePrefs('{"display":"technical","names":"code"}')).toEqual({ display: "technical", names: "code" });
  });
});
