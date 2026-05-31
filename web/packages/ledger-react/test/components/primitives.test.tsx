import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";
import { Button } from "../../src/components/ui/button";
import { Badge } from "../../src/components/ui/badge";

describe("ui primitives smoke", () => {
  test("Button renders its children and is a button by default", () => {
    render(<Button>Click me</Button>);
    const el = screen.getByRole("button", { name: "Click me" });
    expect(el).toBeInTheDocument();
  });

  test("Button forwards variant/size classes via cn", () => {
    render(
      <Button variant="destructive" size="sm">
        Delete
      </Button>,
    );
    const el = screen.getByRole("button", { name: "Delete" });
    expect(el.className).toContain("text-destructive");
  });

  test("Badge renders its children", () => {
    render(<Badge>New</Badge>);
    expect(screen.getByText("New")).toBeInTheDocument();
  });
});
