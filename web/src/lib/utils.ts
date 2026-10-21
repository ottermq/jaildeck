    export function capitalizeFirstLetter(str:string){
        if (!str) return "";
        return str.charAt(0).toUpperCase() + str.slice(1);
    }

    export function normalizeEmptyValue(str:string){
        if (!str) return "—";
        return str;
    }