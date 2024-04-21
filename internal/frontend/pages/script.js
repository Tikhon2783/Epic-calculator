function submitForm(event) {
    event.preventDefault();
    
    const username = document.getElementById('username').value;
    const password = document.getElementById('password').value;
    
    if (!username || !password) {
        alert('Please fill in all fields');
        return;
    }
    
    const form = document.getElementById('authForm');
    const formData = new FormData(form);
    
    console.log("Pretending to send stuf...")
    console.log(formData)
    sessionStorage.setItem("loggedInUser", username)
    console.log("stored:", sessionStorage.getItem('loggedInUser'))
    location.reload();
}

document.addEventListener('DOMContentLoaded', function() {
    const loggedInUser = sessionStorage.getItem('loggedInUser');
    const profileUsername = document.querySelector('.profile-username-btn');
    const logoutButton = document.querySelector('.logout-btn');
    console.log("page loaded", loggedInUser)

    document.querySelector("#authForm").addEventListener("click", function () {
        logoutButton.style.display = "none";
    });

    if (loggedInUser) {
        // User is logged in
        profileUsername.textContent = loggedInUser;

        profileUsername.addEventListener('click', function() {
            logoutButton.style.display = 'block';
            
            logoutButton.addEventListener('click', function() {
                // Perform logout functionality here (e.g., clearing session, redirecting, etc.)
                sessionStorage.removeItem('loggedInUser');
                logoutButton.style.display = 'none';
                profileUsername.textContent = 'Log in';
                profileUsername.addEventListener('click', function() {
                    location.replace('file:///C:/Users/tikho/Documents/Coding/web-projects/authentification-tests/login-page/index.html')
                });
                alert('Logged out.');
            });
        });
    } else {
        // Simulate login process (you should replace this with actual login functionality)
        profileUsername.textContent = "Sign In";
    
        profileUsername.addEventListener('click', function() {
            location.replace('file:///C:/Users/tikho/Documents/Coding/web-projects/authentification-tests/login-page/index.html')
        });
    }
});
