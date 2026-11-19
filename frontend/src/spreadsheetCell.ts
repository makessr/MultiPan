export function spreadsheetCell(value:string){
 const text=value.replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/g,' ').trim()
 // A pasted name must stay text in spreadsheet software.
 return /^[=+@-]/.test(text)?"'"+text:text
}
