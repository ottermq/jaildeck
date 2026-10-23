    export function capitalizeFirstLetter(str:string){
        if (!str) return "";
        return str.charAt(0).toUpperCase() + str.slice(1);
    }

    export function normalizeEmptyValue(str:string){
        if (!str) return "—";
        return str;
    }

    // formatBytes renders a byte count the way zfs list would have printed it
    // without -p. The API sends exact bytes so a client can compare and total
    // them (#2); spelling them for a human is this side's job.
    //
    // Binary units, because that is what zfs means by G: 1.41G of a 1024-based
    // gibibyte, not a billion bytes.
    export function formatBytes(bytes: number): string {
        if (!Number.isFinite(bytes) || bytes < 0) return "—";
        if (bytes === 0) return "0B";
        const units = ["B", "K", "M", "G", "T", "P"];
        let value = bytes;
        let unit = 0;
        while (value >= 1024 && unit < units.length - 1) {
            value /= 1024;
            unit++;
        }
        // One decimal below 10, none above, as zfs does: 1.4G, then 897G.
        const digits = unit === 0 || value >= 10 ? 0 : 1;
        return `${value.toFixed(digits)}${units[unit]}`;
    }

