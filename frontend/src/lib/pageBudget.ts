// #443: a page of many long posts must not format them all. Each formatted post
// holds a tree and its elements; 220 replies of 50 KB each took 3.76 GB of heap.
// Posts reserve their size here when they are formatted and give it back when
// they go, and a post that does not fit stays as written, with a note.

export class PageBudget {
  used = 0
  readonly limit: number

  constructor(limit: number) {
    this.limit = limit
  }

  reserve(nodes: number): boolean {
    if (this.used + nodes > this.limit) return false
    this.used += nodes
    return true
  }

  release(nodes: number) {
    this.used = Math.max(0, this.used - nodes)
  }
}

/** About a dozen worst-case posts (a 50 KB post can be 25,000 nodes), or hundreds of ordinary ones. */
export const pageBudget = new PageBudget(300_000)
